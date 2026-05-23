SHELL := /bin/sh

BIN_NAME := agenrena
LOCAL_BIN := agenrena-cli
DIST_DIR := dist
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64
VERSION ?= $(shell sed -n 's/^[[:space:]]*cliVersion[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' main.go)
TAG ?= v$(VERSION)
LDFLAGS := -s -w

.PHONY: help version check test vet build clean clean-dist dist release release-check tag ensure-tag push-release require-tag

help:
	@printf '%s\n' 'Agenrena CLI release helpers'
	@printf '%s\n' ''
	@printf '%s\n' 'Common targets:'
	@printf '%s\n' '  make check      Run tests and go vet'
	@printf '%s\n' '  make build      Build local ./agenrena-cli'
	@printf '%s\n' '  make dist       Optional local cross-build smoke test'
	@printf '%s\n' '  make release    Run checks, tag, and push main + tag'
	@printf '%s\n' '  make tag        Create annotated git tag matching cliVersion'
	@printf '%s\n' ''
	@printf 'Current version: %s\n' '$(VERSION)'
	@printf 'Release tag:     %s\n' '$(TAG)'

version:
	@printf '%s\n' '$(VERSION)'

check: test vet

test:
	go test ./...

vet:
	go vet ./...

build: check
	go build -trimpath -ldflags "$(LDFLAGS)" -o ./$(LOCAL_BIN) .
	@./$(LOCAL_BIN) version

clean:
	rm -rf ./$(LOCAL_BIN) ./$(DIST_DIR)

clean-dist:
	rm -rf ./$(DIST_DIR)
	mkdir -p ./$(DIST_DIR)

dist: check clean-dist
	@set -eu; \
	for target in $(PLATFORMS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		asset="$(BIN_NAME)-$${os}-$${arch}"; \
		echo "Building $${asset}"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o "$(DIST_DIR)/$${asset}" .; \
	done
	shasum -a 256 ./$(DIST_DIR)/$(BIN_NAME)-* > ./$(DIST_DIR)/checksums.txt
	@printf '%s\n' 'Built release assets:'
	@ls -lh ./$(DIST_DIR)

release:
	$(MAKE) check
	$(MAKE) release-check
	$(MAKE) ensure-tag
	$(MAKE) push-release
	@printf '%s\n' ''
	@printf 'Pushed main and %s. GitHub Actions should build and publish the release assets.\n' '$(TAG)'

release-check:
	@test -n "$(VERSION)" || (echo "Could not read cliVersion from main.go" >&2; exit 1)
	@test "$(TAG)" = "v$(VERSION)" || (echo "TAG ($(TAG)) must match cliVersion ($(VERSION)); expected v$(VERSION)" >&2; exit 1)
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "main" || (echo "Releases must be cut from the main branch." >&2; exit 1)
	@git diff --quiet || (echo "Working tree has unstaged changes; commit or stash before release." >&2; exit 1)
	@git diff --cached --quiet || (echo "Working tree has staged changes; commit or unstage before release." >&2; exit 1)

tag: release-check ensure-tag

ensure-tag:
	@if git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null; then \
		if test "$$(git rev-list -n 1 "$(TAG)")" != "$$(git rev-parse HEAD)"; then \
			echo "Tag $(TAG) already exists but does not point at HEAD." >&2; \
			exit 1; \
		fi; \
		echo "Tag $(TAG) already exists on HEAD."; \
	else \
		git tag -a "$(TAG)" -m "$(TAG)"; \
		echo "Created tag $(TAG)"; \
	fi

push-release: release-check require-tag
	git push origin main
	git push origin "$(TAG)"

require-tag:
	@git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || (echo "Tag $(TAG) does not exist. Run make tag first." >&2; exit 1)
