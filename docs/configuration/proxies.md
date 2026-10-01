# Proxies

Some DNS APIs only accept requests from a fixed address, which does not fit a daemon on a dynamic one. Run a proxy on a small host with a static address, allow that address in the provider's API settings, and give the provider a `proxy` parameter:

```toml
[provider.regru]
type  = "regru"
# ...
proxy = "${PROXY_URL}"   # for example socks5://user:pass@203.0.113.5:1080
```

- Supported schemes are `socks5`, `socks5h`, `http` and `https`, with an optional `user:pass@`; percent-encode special characters in them. With `socks5` and `socks5h` the proxy resolves the API host name.
- Every provider and every reference to it can set its own `proxy`, so different zones can leave through different hosts. `proxy = "direct"` means no proxy at all, whatever `HTTP_PROXY` and `HTTPS_PROXY` say.
- Without `proxy`, a provider connects the way Go does by default, so `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` from the environment apply. With a URL or `direct`, the environment is ignored.
- Retrievers take the same parameter, but it defaults to `direct` and they never follow the environment. Behind a proxy the address service reports the address the proxy connects from, not the address of this host, so give a retriever a proxy only when that is the address you want. With a proxy the retriever's `family` no longer pins the connection, it only checks the reply.
- A malformed URL stops the daemon at startup. The URL is never printed in logs or errors, since it may hold a password.

A complete file is in [proxy.toml](https://github.com/dnspatch/dnspatch/blob/main/examples/proxy.toml).
