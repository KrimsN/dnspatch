package rfc2136

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	// tsigFudge is the clock skew, in seconds, the server may allow between
	// its clock and the signing time (RFC 8945 recommends 300).
	tsigFudge = 300

	defaultPort = "53"
)

// algorithms maps the accepted spellings of a TSIG algorithm to the name the
// library signs with. MD5 is not offered: the library no longer supports it.
var algorithms = map[string]string{
	"hmac-sha1":   dns.HmacSHA1,
	"hmac-sha224": dns.HmacSHA224,
	"hmac-sha256": dns.HmacSHA256,
	"hmac-sha384": dns.HmacSHA384,
	"hmac-sha512": dns.HmacSHA512,
}

// provider publishes an address to one record of one zone through dynamic
// updates.
type provider struct {
	server  string
	zone    string
	name    string
	ttl     uint32
	client  *dns.Client
	keyName string
	algo    string
}

// newProvider validates cfg and builds the provider.
func newProvider(cfg Config) (*provider, error) {
	server, err := serverAddress(cfg.Server)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	zone := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cfg.Zone)), ".")
	if zone == "" || strings.ContainsAny(zone, "/ ") {
		return nil, fmt.Errorf("zone: %q is not a domain name", cfg.Zone)
	}

	label := strings.ToLower(strings.TrimSpace(cfg.RRName))
	if label == "" || strings.HasSuffix(label, ".") {
		return nil, fmt.Errorf(`rr_name: %q must be "@", "*" or a name relative to the zone, without a trailing dot`, cfg.RRName)
	}

	if cfg.TTL < 0 || cfg.TTL > math.MaxInt32 {
		return nil, fmt.Errorf("ttl: %d is out of range", cfg.TTL)
	}

	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("timeout: %s must be positive", cfg.Timeout)
	}

	client := &dns.Client{Net: strings.ToLower(cfg.Protocol), Timeout: cfg.Timeout}
	if client.Net != "tcp" && client.Net != "udp" {
		return nil, fmt.Errorf(`protocol: %q must be "tcp" or "udp"`, cfg.Protocol)
	}

	p := &provider{
		server: server,
		zone:   dns.Fqdn(zone),
		name:   dns.Fqdn(zone),
		ttl:    uint32(cfg.TTL),
		client: client,
	}
	if label != "@" {
		p.name = dns.Fqdn(label + "." + zone)
	}

	if err := p.configureKey(cfg); err != nil {
		return nil, err
	}

	return p, nil
}

// configureKey sets up TSIG signing, or none when neither key parameter is
// given.
func (p *provider) configureKey(cfg Config) error {
	if cfg.KeyName == "" && cfg.KeySecret == "" {
		return nil
	}
	if cfg.KeyName == "" || cfg.KeySecret == "" {
		return errors.New("key_name and key_secret must be set together")
	}

	algo, ok := algorithms[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(cfg.KeyAlgorithm), "."))]
	if !ok {
		return fmt.Errorf("key_algorithm: %q is not one of hmac-sha1, hmac-sha224, hmac-sha256, hmac-sha384, hmac-sha512", cfg.KeyAlgorithm)
	}

	secret := strings.TrimSpace(cfg.KeySecret)
	if _, err := base64.StdEncoding.DecodeString(secret); err != nil {
		return errors.New("key_secret: is not valid base64")
	}

	p.keyName = dns.CanonicalName(cfg.KeyName)
	p.algo = algo
	p.client.TsigSecret = map[string]string{p.keyName: secret}
	return nil
}

// serverAddress turns host or host:port into host:port, adding the default
// port when there is none.
func serverAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("must not be empty")
	}

	if host, port, err := net.SplitHostPort(s); err == nil {
		if host == "" || port == "" {
			return "", fmt.Errorf("%q has an empty host or port", s)
		}
		return s, nil
	}

	host := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if host == "" || strings.ContainsAny(host, "/ ") {
		return "", fmt.Errorf("%q is not a host name or address", s)
	}
	return net.JoinHostPort(host, defaultPort), nil
}

// Update points the configured record(s) at addrs: an A record for V4, an
// AAAA record for V6. Each valid family is written independently; an invalid
// one is left untouched. opts.TTL, when positive, overrides the configured
// TTL.
func (p *provider) Update(ctx context.Context, addrs plugin.Addresses, opts plugin.RecordOptions) error {
	if !addrs.V4.IsValid() && !addrs.V6.IsValid() {
		return errors.New("no address to write")
	}

	ttl := p.ttl
	if opts.TTL > 0 {
		ttl = uint32(min(opts.TTL.Round(time.Second)/time.Second, math.MaxInt32))
	}

	var errs []error
	if addrs.V4.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V4, ttl))
	}
	if addrs.V6.IsValid() {
		errs = append(errs, p.setOne(ctx, addrs.V6, ttl))
	}
	return errors.Join(errs...)
}

// setOne replaces the record set of addr's type at the name with a single
// record holding addr.
func (p *provider) setOne(ctx context.Context, addr netip.Addr, ttl uint32) error {
	rr := p.record(addr.Unmap(), ttl)
	what := fmt.Sprintf("update %s %s", dns.TypeToString[rr.Header().Rrtype], strings.TrimSuffix(p.name, "."))

	msg := new(dns.Msg)
	msg.SetUpdate(p.zone)
	msg.RemoveRRset([]dns.RR{rr})
	msg.Insert([]dns.RR{rr})
	if p.keyName != "" {
		msg.SetTsig(p.keyName, p.algo, tsigFudge, time.Now().Unix())
	}

	reply, _, err := p.client.ExchangeContext(ctx, msg, p.server)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if reply.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("%s: server answered %s", what, dns.RcodeToString[reply.Rcode])
	}
	if p.keyName != "" && reply.IsTsig() == nil {
		return fmt.Errorf("%s: the server did not sign its reply", what)
	}
	return nil
}

// record builds the A or AAAA record for addr.
func (p *provider) record(addr netip.Addr, ttl uint32) dns.RR {
	hdr := dns.RR_Header{Name: p.name, Class: dns.ClassINET, Ttl: ttl}
	if addr.Is4() {
		hdr.Rrtype = dns.TypeA
		return &dns.A{Hdr: hdr, A: net.IP(addr.AsSlice())}
	}

	hdr.Rrtype = dns.TypeAAAA
	return &dns.AAAA{Hdr: hdr, AAAA: net.IP(addr.AsSlice())}
}
