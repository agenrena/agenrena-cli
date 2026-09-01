# Release

The source of truth for the whole CLI binary version is `buildinfo.Version` in
`internal/buildinfo/version.go`.
Release tags should match that version with a leading `v`, for example
`buildinfo.Version = "0.13.0"` pairs with tag `v0.13.0`.

Do not use protocol, route, media, persisted-state schema, or plugin cachebuster
versions as CLI release versions. Feature availability is negotiated through
capability flags such as `turnUpdates`, not inferred from the release version.

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
matching `v<version>` tag if needed, pushes `main`, and pushes the tag. The tag push
should trigger GitHub Actions to build the assets consumed by `install.sh`:

```text
agenrena-darwin-arm64
agenrena-darwin-amd64
agenrena-linux-arm64
agenrena-linux-amd64
agenrena-rtc-helper-darwin-arm64.tar.gz
agenrena-rtc-helper-darwin-amd64.tar.gz
agenrena-rtc-helper-linux-arm64
agenrena-rtc-helper-linux-amd64
```

The macOS helper archives include the helper and its adjacent `libopus` and
`libsoxr` dynamic libraries. Linux helper assets are statically linked single
binaries. `install.sh` installs the matching helper together with the CLI.
