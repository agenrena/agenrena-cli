# syntax=docker/dockerfile:1
FROM --platform=$TARGETPLATFORM golang:1.26-alpine AS build

RUN apk add --no-cache build-base opus-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build \
    -tags "nolibopusfile netgo osusergo" \
    -trimpath \
    -ldflags '-s -w -linkmode external -extldflags "-static"' \
    -o /out/agenrena-rtc-helper \
    ./cmd/agenrena-rtc-helper

FROM scratch AS export
COPY --from=build /out/agenrena-rtc-helper /

