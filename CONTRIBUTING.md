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
`feat(plugins/providers/regru)`. Omit it for changes that span the repository.

Write the description in the imperative mood, in lower case, with no trailing
period: `add backoff to failing providers`, not `Added backoff.`.

A breaking change carries a `!` before the colon and a `BREAKING CHANGE:`
footer explaining the migration. The public contract in `plugin/` is the part
most likely to need one.

`feat`, `fix` and `perf` commits appear in the release notes; every other type
is filtered out by `.goreleaser.yaml`.

## Issues, branches and Linear

Bug reports, questions and ideas go to [GitHub Issues](https://github.com/dnspatch/dnspatch/issues).
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

## Documentation

The documentation site is built with [MkDocs Material](https://squidfunnel.github.io/mkdocs-material/) from `docs/` and `mkdocs.yml`, and published to GitHub Pages from `main`. To preview it locally:

```
pip install -r docs/requirements.txt
mkdocs serve
```

`mkdocs build` runs in strict mode in CI: a broken link or a page missing from the `nav` fails the build. `docs/PARAMETERS.md` and the table of build tags in `docs/deployment/building.md` are generated (see below); everything else is written by hand. The configs on the examples page are included from `examples/` with `--8<-- "path"`, and the build also writes `llms.txt` and `llms-full.txt` for AI assistants; a new page goes into the `nav` and, to be listed there, into the `llmstxt` sections of `mkdocs.yml`.

## Writing a plugin

The step-by-step guide lives in the documentation: [Writing a plugin](https://dnspatch.github.io/dnspatch/development/writing-a-plugin/), source in [docs/development/writing-a-plugin.md](docs/development/writing-a-plugin.md). After adding a plugin or changing a `Config`, run `go generate ./...` and commit the result: it rewrites `docs/PARAMETERS.md`, `dnspatch.toml.example`, `plugins/all` and the table of build tags. The tests `TestCommittedFilesAreCurrent` of `cmd/gendoc` (which needs `-tags notify_all`) and `cmd/genplugins` fail when the committed files are out of date.
