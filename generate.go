// Package dnspatch is the root of the dnspatch module and holds no code; it
// exists so that go generate has a place to run the repository-wide generators
// from.
//
// dnspatch is a dynamic DNS daemon: it watches the public IP address and
// patches DNS records when it changes. The daemon is the command
// github.com/KrimsN/dnspatch/cmd/dnspatch. Its retrievers and providers are
// plugins that implement the interfaces of the public package
// github.com/KrimsN/dnspatch/plugin; the built-in ones live under
// github.com/KrimsN/dnspatch/plugins.
//
// Source, releases and container images: https://github.com/KrimsN/dnspatch.
package dnspatch

//go:generate go run ./cmd/gendoc
