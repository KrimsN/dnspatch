package regru

// Config holds the parameters of the regru provider.
type Config struct {
	Username string `toml:"username" required:"true" doc:"REG.RU account login used for API calls"`
	Password string `toml:"password" required:"true" doc:"API password; set an alternative password for the API in the REG.RU account and allow the address dnspatch runs from"`
	Zone     string `toml:"zone" required:"true" doc:"Domain name of the zone, for example example.com"`
	RRName   string `toml:"rr_name" required:"true" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	BaseURL  string `toml:"base_url" default:"https://api.reg.ru/api/regru2" doc:"Base URL of the API"`
}
