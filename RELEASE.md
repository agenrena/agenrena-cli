# Release

The source of truth for the CLI binary version is `cliVersion` in `main.go`.
Release tags should match that version with a leading `v`, for example
`cliVersion = "0.4.1"` pairs with tag `v0.4.1`.

Useful release commands:

```sh
make check
make build
make dist
```

To cut a release:

```sh
make release
make tag
git push origin v0.4.1
make publish
```

`make dist` builds the assets consumed by `install.sh`:

```text
dist/agenrena-darwin-arm64
dist/agenrena-darwin-amd64
dist/agenrena-linux-arm64
dist/agenrena-linux-amd64
dist/checksums.txt
```

`make publish` requires the GitHub CLI (`gh`) and creates the GitHub release
using the assets in `dist/`.
