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
`feat(plugins/selectel)`. Omit it for changes that span the repository.

Write the description in the imperative mood, in lower case, with no trailing
period: `add backoff to failing providers`, not `Added backoff.`.

A breaking change carries a `!` before the colon and a `BREAKING CHANGE:`
footer explaining the migration. The public contract in `plugin/` is the part
most likely to need one.

`feat`, `fix` and `perf` commits appear in the release notes; every other type
is filtered out by `.goreleaser.yaml`.

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

<!-- TODO: how to write a plugin, review expectations -->
