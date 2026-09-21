package selectelv1

// Config holds the parameters of the selectel_v1 provider.
type Config struct {
	APIToken string `toml:"api_token" required:"true" doc:"Static API token (X-Token); create one in the control panel under Access, API keys"`
	Zone     string `toml:"zone" required:"true" doc:"Domain name of the zone, for example example.com"`
	RRName   string `toml:"rr_name" required:"true" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	TTL      int    `toml:"ttl" default:"3600" doc:"TTL of the record in seconds"`
	BaseURL  string `toml:"base_url" default:"https://api.selectel.ru/domains/v1" doc:"Base URL of the API"`
}
