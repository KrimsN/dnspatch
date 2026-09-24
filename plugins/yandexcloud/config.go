package yandexcloud

import "github.com/KrimsN/dnspatch/internal/httpx"

// Config holds the parameters of the yandexcloud provider.
type Config struct {
	Key     string `toml:"key,required,secret" example:"${YANDEX_CLOUD_KEY}" doc:"Authorized key of the service account as JSON, the file that yc iam key create writes. The account needs the dns.editor role on the folder of the zone"`
	ZoneID  string `toml:"zone_id,required" example:"dns1234567890abcdefgh" doc:"ID of the DNS zone, shown in the Cloud DNS console or by yc dns zone list"`
	RRName  string `toml:"rr_name,required" example:"home" doc:"Record name relative to the zone: @ for the apex, * for a wildcard, or a label such as home"`
	TTL     int    `toml:"ttl" default:"300" doc:"TTL in seconds for a record this provider creates; an existing record keeps its own TTL"`
	IAMURL  string `toml:"iam_url" default:"https://iam.api.cloud.yandex.net/iam/v1/tokens" doc:"URL of the IAM API endpoint that exchanges a signed JWT for an IAM token"`
	BaseURL string `toml:"base_url" default:"https://dns.api.cloud.yandex.net/dns/v1" doc:"Base URL of the Cloud DNS API"`

	httpx.ProxyConfig
}
