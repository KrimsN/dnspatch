# Contributing

## Commit messages

This repository follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>(<scope>): <description>
```

| Type | Use for |
|------|---------|
| `feat` | a new capability: a plugin, a config option, a flag |
| `fix` | a bug fix |
| `docs` | documentation only, including `README.md` and this file |
| `test` | adding or reworking tests |
| `refactor` | a change that neither fixes a bug nor adds a capability |
| `perf` | a change made for performance |
| `build` | the module, its dependencies or the release build |
| `ci` | workflows, the linter and their configuration |
| `chore` | anything else, including dependency bumps |

The scope is the package the change belongs to: `feat(plugin)`, `fix(runner)`,
`feat(plugins/regru)`. Omit it for changes that span the repository.

Write the description in the imperative mood, in lower case, with no trailing
period: `add backoff to failing providers`, not `Added backoff.`.

A breaking change carries a `!` before the colon and a `BREAKING CHANGE:`
footer explaining the migration. The public contract in `plugin/` is the part
most likely to need one.

`feat`, `fix` and `perf` commits appear in the release notes; every other type
is filtered out by `.goreleaser.yaml`.

## Issues, branches and Linear

Bug reports, questions and ideas go to [GitHub Issues](https://github.com/KrimsN/dnspatch/issues).
That is all an outside contributor needs.

The maintainer plans the work in [Linear](https://linear.app), a task tracker: a
project `dnspatch` in a team whose key is `DNS`. Every planned task there is an
issue with an ID such as `DNS-13`, and the pull requests of this repository
refer to those IDs, which is why you will see them in branch names and PR
descriptions. The board is the maintainer's working tool; you do not need
access to it, and nothing in this repository depends on it.

If you have no Linear ID, name the branch `<type>/<slug>` with the same types as
commits (`feat/retry-after`, `fix/regru-timeout`) and leave the `Linear:` line
out of the description. The maintainer links the pull request to a task, if it
belongs to one.

For work that does belong to a task, the convention is:

- **Branch:** `<type>/dns-<N>-<slug>`, for example `fix/dns-13-review-e`. The
  type is the commit type from the table above, `<N>` is the number of the
  issue. Linear recognises the ID in the branch name and attaches the pull
  request to the issue by itself.
- **Pull request description:** a line `Linear: DNS-<N>`. Write `Closes DNS-<N>`
  when the pull request finishes the task, so that merging it closes the issue,
  and `Part of DNS-<N>` when more pull requests are to follow. Both are Linear's
  keywords; either way the issue links back to the pull request.
- **Pull request title:** the Conventional Commits title, without the ID. The
  title ends up in the release notes, and `DNS-13` means nothing to a reader
  outside the tracker.

Pull requests without a task, such as dependency bumps from Dependabot and
maintenance of the workflows, carry no ID.

## Pull requests

The pull request title follows the same convention — it becomes the subject of
the squash-merge commit, so it ends up in the history and in the release notes.

Before opening one, make sure these pass:

```
go build ./...
go vet ./...
go test -race ./...
golangci-lint run ./...
```

CI also runs `govulncheck` on the newest Go release. The Go standard library is
part of the binary, so an old local toolchain can report fixed bugs of it; update
Go before treating such a finding as yours.

## Writing a plugin

A plugin is a package under `plugins/` with a configuration struct and a
constructor. The steps below use a provider; a retriever differs only in the
interface it implements (`GetAddresses` instead of `Update`) and in the
`RegisterRetriever` call. `plugins/regru` (provider) and `plugins/ifconfigco`
(retriever) are complete examples to copy from.

### 1. Lay out the package

```
plugins/example/
  example.go        package doc, Name constant, init() registration
  config.go         the Config struct
  provider.go       the implementation
  provider_test.go  tests against httptest
```

The package doc comment says what the service is, what the plugin can and
cannot do with it (for example, that the API cannot set a TTL), and any
service rules the user should know, such as rate limits. It ends up on
pkg.go.dev.

### 2. Declare the configuration

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

#### Order and names of parameters

The order of the fields of `Config` is the order of the parameters in
`docs/PARAMETERS.md`, in `config.toml.example` and in the signature that
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

### 3. Register the plugin

```go
const Name = "example"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
```

The name must be unique among providers (and among retrievers): registering a
name twice panics at start-up. Add a blank import of the package to
`plugins/all/all.go`, otherwise the binary does not contain it.

### 4. Implement it

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

### 5. Test it

Test against `httptest`, and cover at least: the success path for both address
families (for a provider), an HTTP error status, a malformed reply, a
cancelled context, and every validation error of the constructor.
Use `plugin.NewRegistry()` rather than `plugin.Default` in tests, so that
registrations do not leak between them.

### 6. Regenerate the documentation

`docs/PARAMETERS.md` and `config.toml.example` are generated from the tags. After
adding a plugin or changing a `Config`, run

```
go generate ./...
```

and commit the result. The test `TestCommittedFilesAreCurrent` (part of
`go test ./...`, so of CI) fails when the committed files are out of date.
Never edit the generated files by hand.

### Plugins outside this repository

`plugin` is a public package, so a plugin can also live in your own module and
register itself in `plugin.Default` or in a registry you create. Note that the
configuration loader and the runner are internal packages in `v0.x`: a program
of your own has to build and drive the instances itself, so for most plugins a
pull request here is the easier route.
