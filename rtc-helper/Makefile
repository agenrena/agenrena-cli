SHELL := /bin/sh

.PHONY: test build linux-amd64 linux-arm64

test:
	go test -tags nolibopusfile ./...

build:
	go build -tags nolibopusfile -trimpath -o agenrena-rtc-helper ./cmd/agenrena-rtc-helper

linux-amd64:
	docker buildx build --platform linux/amd64 --target export --output type=local,dest=dist/linux-amd64 .

linux-arm64:
	docker buildx build --platform linux/arm64 --target export --output type=local,dest=dist/linux-arm64 .

