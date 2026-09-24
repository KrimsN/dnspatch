package dyndns2

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the dyndns2 provider.
type Config struct {
	BaseURL   string `toml:"base_url" required:"true" example:"https://api.nic.ru/dyndns/update" doc:"Update URL of the service, for example https://api.nic.ru/dyndns/update for NIC.RU or https://updates.dnsomatic.com/nic/update for DNS-O-Matic"`
	Username  string `toml:"username" required:"true" doc:"Login for HTTP Basic authentication; for NIC.RU the login of the contract or of the account allowed to use Dynamic DNS"`
	Password  string `toml:"password" required:"true" example:"${PASSWORD}" doc:"Password for HTTP Basic authentication; some services issue a separate update password"`
	Hostname  string `toml:"hostname" required:"true" example:"home.example.com" doc:"Full domain name of the record to update. NIC.RU changes the A records with this name in every zone of the contract"`
	IPParam   string `toml:"ip_param" default:"myip" doc:"Query parameter that carries the IPv4 address"`
	IPv6Param string `toml:"ipv6_param" default:"ipv6" doc:"Query parameter that carries the IPv6 address; both addresses go in one request, so the service must know this parameter to update an AAAA record. Set it to the same name as ip_param for a service that takes both addresses in one parameter, separated by a comma. A family the retrievers did not report is not sent, and the service may then fall back to the address the request came from"`
	UserAgent string `toml:"user_agent" default:"dnspatch" doc:"User-Agent header; the protocol asks clients to identify themselves, and some services refuse a request without one"`

	httpx.ProxyConfig
}
