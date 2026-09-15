package main

import "testing"

func TestInstallCommandForOS(t *testing.T) {
	if got := installCommandForOS("windows"); got != windowsInstallCommand {
		t.Fatalf("Windows install command = %q", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if got := installCommandForOS(goos); got != unixInstallCommand {
			t.Fatalf("%s install command = %q", goos, got)
		}
	}
}
