package selectel

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the selectel provider.
type Config struct {
	AccountID   string `toml:"account_id,required" doc:"Selectel account ID (the domain name used for API auth), shown in the top right corner of the Control panel"`
	Username    string `toml:"username,required" doc:"Name of the service user used for API calls"`
	Password    string `toml:"password,required,secret" example:"${PASSWORD}" doc:"Password of the service user"`
	ProjectName string `toml:"project_name,required" doc:"Name of the project the DNS zone belongs to"`
	Zone        string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone, for example example.com"`
	RRName      string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	TTL         int    `toml:"ttl" default:"60" doc:"TTL in seconds for a record this provider creates; an existing record keeps its own TTL. Selectel accepts 60 to 604800"`
	AuthURL     string `toml:"auth_url" default:"https://cloud.api.selcloud.ru/identity/v3" doc:"Base URL of the identity (Keystone) API used to obtain a project IAM token"`
	BaseURL     string `toml:"base_url" default:"https://api.selectel.ru/domains/v2" doc:"Base URL of the DNS Hosting API"`

	httpx.ProxyConfig
}
