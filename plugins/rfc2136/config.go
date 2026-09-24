package rfc2136

import "time"

// Config holds the parameters of the rfc2136 provider.
type Config struct {
	Server       string        `toml:"server,required" example:"ns1.example.com:53" doc:"Address of the name server that accepts updates, as host or host:port. The port defaults to 53. It must be the primary (master) server of the zone or one that forwards updates to it"`
	Zone         string        `toml:"zone,required" example:"example.com" doc:"Domain name of the zone the server is authoritative for, for example example.com"`
	RRName       string        `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	KeyName      string        `toml:"key_name" example:"dnspatch-key" doc:"Name of the TSIG key used to sign updates, as configured on the server. Leave empty (together with key_secret) to send unsigned updates, which only fits a server that authorizes by client address"`
	KeySecret    string        `toml:"key_secret,secret" example:"${TSIG_SECRET}" doc:"Base64-encoded secret of the TSIG key, as printed by tsig-keygen"`
	KeyAlgorithm string        `toml:"key_algorithm" default:"hmac-sha256" doc:"TSIG algorithm of the key: hmac-sha1, hmac-sha224, hmac-sha256, hmac-sha384 or hmac-sha512"`
	TTL          int           `toml:"ttl" default:"300" doc:"TTL in seconds of the record this provider writes"`
	Protocol     string        `toml:"protocol" default:"tcp" doc:"Transport of the update: tcp or udp. There is no fallback from one to the other"`
	Timeout      time.Duration `toml:"timeout" default:"10s" doc:"How long to wait for the server to answer an update"`
}
