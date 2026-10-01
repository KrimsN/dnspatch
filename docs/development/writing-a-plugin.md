# Writing a plugin

The `plugin` package is public on purpose. Writing a retriever, a provider or a notifier means implementing a small interface and registering it. The easy route is a pull request to this repository; a plugin can also [live in your own module](external-plugins.md).

## The contract

```go
type Retriever interface {
	GetAddresses(ctx context.Context) (Addresses, error)
}

type Addresses struct{ V4, V6 netip.Addr }
type RecordOptions struct{ TTL time.Duration }

type Provider interface {
	Update(ctx context.Context, addrs Addresses, opts RecordOptions) error
}
```

`Addresses` carries both families at once: an invalid (zero) `V4` or `V6` means that family is not provided (a `Retriever`) or left untouched (a `Provider`), which lets one call report or update both an A and an AAAA address, or just one of them. `RecordOptions` carries options such as `TTL`, which a provider ignores when its service does not support it.

!!! note "Migrating from the single-address interfaces"
    A `Retriever` written against the old `GetIPAddress(ctx) (netip.Addr, error)`: return the single address in the matching field of `Addresses` (`V4` if `addr.Is4()`, `V6` otherwise) and leave the other at its zero value; a plugin that only ever handles one family keeps working unchanged. A retriever whose service is itself dual-stack can fill both fields in one call, as the four `family = "dual"` retrievers built into dnspatch do.

    A `Provider` written against the older `SetIPAddress(ctx, addr netip.Addr) error`: write the same record for each family that is valid (`addrs.V4.IsValid()`, `addrs.V6.IsValid()`) instead of branching on `addr.Is4()`; a plugin that only ever handled one family keeps working unchanged as long as it ignores the family it does not expect.


A plugin is a configuration struct plus a constructor, in a package under `plugins/notifiers/`, `plugins/retrievers/` or `plugins/providers/` (by kind). The steps below use a provider; a retriever differs only in the
interface it implements (`GetAddresses` instead of `Update`) and in the
`RegisterRetriever` call. `plugins/providers/regru` (provider) and `plugins/retrievers/ifconfigco`
(retriever) are complete examples to copy from.

## 1. Lay out the package

```
plugins/providers/example/
  example.go        package doc, Name constant, init() registration
  config.go         the Config struct
  provider.go       the implementation
  provider_test.go  tests against httptest
```

The package doc comment says what the service is, what the plugin can and
cannot do with it (for example, that the API cannot set a TTL), and any
service rules the user should know, such as rate limits. It ends up on
pkg.go.dev.

## 2. Declare the configuration

Every parameter is a field of an exported `Config` struct. The struct tags are
the single source of truth: the decoder, the parameter reference in
`docs/PARAMETERS.md` and the example configuration are all built from them.

```go
type Config struct {
	BaseURL string `toml:"base_url" default:"https://api.example.com" doc:"Base URL of the API"`
	Zone    string `toml:"zone,required" example:"example.com" doc:"Domain name of the zone"`
	Token   string `toml:"token,required,secret" example:"${EXAMPLE_TOKEN}" doc:"API token with edit rights on the zone"`

	httpx.ProxyConfig
}
```

The order of the fields is the order of the parameters everywhere they are
listed, so keep to the scheme under "Order and names of parameters" below.

| Tag | Meaning | Why it matters |
|-----|---------|----------------|
| `toml:"name"` | the key in the configuration file | without it the key is the lower-cased field name; spell it out so that renaming a field never renames a parameter |
| `toml:"name,required"` | the parameter must be set | a missing required parameter stops the daemon at startup with an error naming it; without the option a forgotten token becomes an unauthorised request to the API |
| `toml:"name,secret"` | the value is a password, key, token or a URL with a login | `--check-config` lists the parameters that tell providers of one type apart; a secret one is printed as `***`, and only when nothing else differs, while a parameter without the option is printed as it is. A test fails for a parameter whose `example` is a `${NAME}` reference but that lacks the option |
| `default:"value"` | the value used when the parameter is omitted | shown in the reference and the example file; the daemon and the docs cannot disagree, since both read the same tag |
| `doc:"text"` | the description shown in `docs/PARAMETERS.md` and as a comment in the example file | a parameter without it appears as "no description" in the reference |
| `example:"value"` | the value the example file shows | required for a required parameter that is not a plain string; for a secret, use a `${NAME}` reference |

Notes:

- Options follow the key after a comma, the way `encoding/json` writes
  `json:"name,omitempty"`. An unknown or repeated option stops the process at
  registration, so a typo cannot turn into a flag that silently does nothing.
- Embed `httpx.ProxyConfig` in a provider to get the `proxy` parameter
  (`httpx.DirectProxyConfig` in a retriever, where the default is `direct`) and
  build the client with `httpx.NewClient`. Do not read `HTTP_PROXY` yourself.
- Values of type `time.Duration`, `netip.Addr` and anything implementing
  `encoding.TextUnmarshaler` are parsed from strings.
- Unknown parameters and missing required ones are reported by the decoder; the
  constructor only checks what the tags cannot express, such as that `base_url`
  is an `http(s)` URL. Prefix its errors with the parameter name.

### Order and names of parameters

The order of the fields of `Config` is the order of the parameters in
`docs/PARAMETERS.md`, in `dnspatch.toml.example` and in the signature that
`--check-config` prints for a provider. So that a reader finds the same thing in
the same place in every plugin, the order goes from the general to the
particular, with secrets and `proxy` at the end:

1. The entry point: `base_url` (`server` for a name server given as host or
   host:port).
2. The record: `zone` or `zone_id`, then `rr_name` or `hostname`.
3. What is written to the record: `ttl`, and the query parameters that carry the
   address (`ip_param`, `ipv6_param`).
4. The scope of the account: `project_name`, `account_id`.
5. Credentials: `username`, `password`; a key that is not a password comes here
   too (`key`, or the TSIG parameters `key_name`, `key_algorithm`, `key_secret`).
6. The technical side of access and transport: `auth_url`, `user_agent`,
   `protocol`, `timeout`.
7. `proxy`, the embedded `httpx.ProxyConfig`, always the last.

A parameter that fits no group goes next to the one closest to it in meaning. A
retriever has fewer of them: `base_url`, `family`, then `proxy`.

Name a parameter as its counterparts in the other plugins are named:

| Meaning | Name | Notes |
|---------|------|-------|
| URL of the service | `base_url` | an `http(s)` URL, defaulting to the public API |
| Login of the account | `username` | not `login` or `user`, even where the service's own API calls it that |
| Secret of the login | `password` | marked `secret` |
| Zone of the domain | `zone` | the domain name; `zone_id` when the service addresses a zone by an ID |
| Record in the zone | `rr_name` | relative to the zone (`@`, `*`, `home`); `hostname` when the service takes the full domain name |
| Lifetime of the record | `ttl` | in seconds, an `int` |
| Identity service that issues the token | `auth_url` | a base URL the plugin appends the path to; a full token endpoint is named after the service, as `iam_url` of yandexcloud is |
| Proxy of the requests | `proxy` | comes from the embedded `httpx.ProxyConfig` |

The names of `zone` and `zone_id`, of `rr_name` and `hostname`, and of `server`
and `base_url` stay apart on purpose: they mean different things (a name or an
ID, a name in the zone or a full name, host:port or a URL).

## 3. Register the plugin

```go
const Name = "example"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
```

The name must be unique among providers (and among retrievers): registering a
name twice panics at start-up. The name is also the build tag that selects the
plugin (see step 6), so it must be unique among all plugins, and a package
registers exactly one. Register it with a string literal or a `Name` constant,
which is how `go generate` reads it without compiling the package.

A notification backend is a plugin as well: it implements `plugin.Notifier`,
registers with `plugin.RegisterNotifier`, and its configuration struct embeds
`plugin.NotifierCommon`, which supplies `topic_prefix`. Because it brings the
client library of a broker, it is off in a plain build; `notify_all` or its own
tag turns it on.

## 4. Implement it

Every network call a plugin makes must be bound to the `ctx` it receives, for
example with `http.NewRequestWithContext(ctx, ...)`. The runner puts a deadline
(30 seconds by default) on every `GetAddresses` and `Update` call and
cancels the context on shutdown; a plugin that ignores `ctx` can stall its
instance indefinitely. An HTTP client field on the plugin is fine for tests
against `httptest`, but it must not replace the context.

Make the plugin testable without the network:

- Keep the constructor as `newProvider(cfg Config, client *http.Client)`: the
  registered function passes `nil` to get the real client, a test passes
  `srv.Client()`.
- Take the base URL from the configuration, so a test can point it at an
  `httptest.Server`. Never hard-code the host of the service.
- Cap the size of a response you read (`io.LimitReader`) and close bodies.
- Do not put secrets into error messages or logs. That includes proxy URLs.
- A provider decides the record type from the field of `plugin.Addresses`:
  `V4` is an `A` record, `V6` an `AAAA` record. An invalid (zero) field means
  that family is not touched — at least one of the two is always valid.
  Writing a record that already holds the address must succeed and change
  nothing: after a restart the daemon writes to every provider without
  knowing what it wrote before.
- A retriever returns a `plugin.Addresses` with at least one valid global
  unicast field, and an error otherwise. A single-family retriever leaves the
  other field at its zero value; a retriever whose service is itself
  dual-stack can fill both in one call (see the built-in `family = "dual"`
  retrievers for the pattern: one HTTP request per family, joined with
  `errors.Join` if either fails).

## 5. Test it

Test against `httptest`, and cover at least: the success path for both address
families (for a provider), an HTTP error status, a malformed reply, a
cancelled context, and every validation error of the constructor.
Use `plugin.NewRegistry()` rather than `plugin.Default` in tests, so that
registrations do not leak between them.

## 6. Regenerate the generated files

Several files are generated. After adding a plugin or changing a `Config`, run

```
go generate ./...
```

and commit the result. It runs two generators:

- `cmd/genplugins` finds the plugins by their registration call and writes,
  into `plugins/all`, a file that imports each one under its build tag, and a
  catalog that lets a build without a plugin say which tag brings it. It also
  updates the table of build tags in [Building from source](../deployment/building.md). This is what makes a new
  plugin selectable with `-tags`, and part of the default build, with no edit
  of a list.
- `cmd/gendoc` writes `docs/PARAMETERS.md` and `dnspatch.toml.example` from the
  struct tags. It has to see every plugin, so `go generate` builds it with the
  `notify_all` tag; it refuses to write from a build that lacks one.

The tests `TestCommittedFilesAreCurrent` of both (part of `go test ./...`, so
of CI) fail when the committed files are out of date; the one of `cmd/gendoc`
needs `-tags notify_all` to run, and CI passes it. Never edit the generated
files by hand.
