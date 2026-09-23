package netif

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	familyIPv4 = "ipv4"
	familyIPv6 = "ipv6"
	// familyDual reads both families in one retriever.
	familyDual = "dual"
)

// cgnat is the shared address space of carrier-grade NAT (RFC 6598). It is
// not covered by netip.Addr.IsPrivate, but an address from it is not
// reachable from the internet either.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// lookupFunc returns the addresses assigned to the named interface.
type lookupFunc func(name string) ([]net.Addr, error)

type retriever struct {
	name    string
	family  string
	network netip.Prefix
	lookup  lookupFunc
}

// newRetriever validates cfg and builds the retriever. A nil lookup reads
// the addresses from the operating system; tests pass their own.
func newRetriever(cfg Config, lookup lookupFunc) (*retriever, error) {
	family := strings.ToLower(cfg.Family)
	if family != familyIPv4 && family != familyIPv6 && family != familyDual {
		return nil, fmt.Errorf(`family: must be "ipv4", "ipv6" or "dual", got %q`, cfg.Family)
	}

	if strings.TrimSpace(cfg.Name) == "" {
		return nil, errors.New("name: must not be empty")
	}

	r := &retriever{name: cfg.Name, family: family, lookup: lookup}

	if cfg.Network != "" {
		prefix, err := netip.ParsePrefix(cfg.Network)
		if err != nil {
			return nil, fmt.Errorf("network: %w", err)
		}
		r.network = prefix.Masked()
	}

	if r.lookup == nil {
		r.lookup = osLookup
	}

	return r, nil
}

// osLookup reads the addresses of the named interface from the operating
// system.
func osLookup(name string) ([]net.Addr, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}

	return iface.Addrs()
}

// GetAddresses reads the interface. For "dual" both families must be found:
// a partial result is not what "dual" was configured for.
func (r *retriever) GetAddresses(ctx context.Context) (plugin.Addresses, error) {
	if err := ctx.Err(); err != nil {
		return plugin.Addresses{}, err
	}

	raw, err := r.lookup(r.name)
	if err != nil {
		return plugin.Addresses{}, fmt.Errorf("interface %q: %w", r.name, err)
	}

	addrs := parseAddrs(raw)

	var out plugin.Addresses

	if r.family != familyIPv6 {
		if out.V4, err = r.selectAddr(addrs, false); err != nil {
			return plugin.Addresses{}, err
		}
	}

	if r.family != familyIPv4 {
		if out.V6, err = r.selectAddr(addrs, true); err != nil {
			return plugin.Addresses{}, err
		}
	}

	return out, nil
}

// parseAddrs converts the addresses the OS reports into netip.Addr, dropping
// entries of a kind other than *net.IPNet or *net.IPAddr.
func parseAddrs(raw []net.Addr) []netip.Addr {
	addrs := make([]netip.Addr, 0, len(raw))

	for _, a := range raw {
		var ip net.IP

		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}

		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr.Unmap())
		}
	}

	return addrs
}

// selectAddr picks the address to publish out of everything on the
// interface: the numerically lowest one among the public addresses of the
// wanted family inside the configured network. The lowest is an arbitrary
// but stable choice; the operating system does not report which addresses
// are temporary, so the result cannot prefer a permanent one, and the
// network parameter is the way to be explicit.
func (r *retriever) selectAddr(addrs []netip.Addr, v6 bool) (netip.Addr, error) {
	var best netip.Addr

	for _, addr := range addrs {
		if addr.Is6() != v6 || !isPublic(addr) {
			continue
		}
		if r.network.IsValid() && !r.network.Contains(addr) {
			continue
		}
		if !best.IsValid() || addr.Less(best) {
			best = addr
		}
	}

	if best.IsValid() {
		return best, nil
	}

	family := familyIPv4
	if v6 {
		family = familyIPv6
	}

	if r.network.IsValid() {
		return netip.Addr{}, fmt.Errorf("interface %q has no public %s address in %s", r.name, family, r.network)
	}

	return netip.Addr{}, fmt.Errorf("interface %q has no public %s address", r.name, family)
}

// isPublic reports whether addr is one that can be published in public DNS.
func isPublic(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr) && addr.Zone() == ""
}
