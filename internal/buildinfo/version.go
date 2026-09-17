// Package buildinfo exposes metadata for the Agenrena CLI release binary.
package buildinfo

// Version is the single source of truth for the version of the whole CLI.
// Protocol and persisted-state schema versions are defined beside their own
// contracts and must not be used as release versions.
const Version = "0.15.3"
