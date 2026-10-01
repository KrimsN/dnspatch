# Plugins outside the repository

`plugin` is a public package, so a plugin can also live in your own module and register itself in `plugin.Default` or in a registry you create.

!!! warning "Limitations in v0.x"
    The configuration loader and the runner are internal packages in `v0.x`: a program of your own has to build and drive the instances itself instead of reusing `dnspatch`, so for most plugins a pull request to this repository is the easier route. The plugin contract itself is the [`plugin` package](https://pkg.go.dev/github.com/dnspatch/dnspatch/plugin).

The easy route is a pull request: see [Writing a plugin](writing-a-plugin.md) for the layout, the struct tags and the generators, and [CONTRIBUTING.md](https://github.com/dnspatch/dnspatch/blob/main/CONTRIBUTING.md) for commit and pull request conventions.
