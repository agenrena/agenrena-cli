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

`make dist` is only a local cross-build smoke test. GitHub Actions builds the
release assets from the pushed tag.

To cut a release:

```sh
make release
```

`make release` runs checks, verifies the working tree is clean, creates the
`v<cliVersion>` tag if needed, pushes `main`, and pushes the tag. The tag push
should trigger GitHub Actions to build the assets consumed by `install.sh`:

```text
agenrena-darwin-arm64
agenrena-darwin-amd64
agenrena-linux-arm64
agenrena-linux-amd64
```
