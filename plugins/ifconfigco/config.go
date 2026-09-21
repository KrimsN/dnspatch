package ifconfigco

// Config holds the parameters of the ifconfigco retriever.
type Config struct {
	BaseURL string `toml:"base_url" default:"https://ifconfig.co" doc:"Base URL of the service; change it to use a self-hosted instance"`
	Family  string `toml:"family" default:"ipv4" doc:"IP family to ask for: ipv4 or ipv6"`
}
