# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build

ARG OPUS_VERSION=1.5.2
ARG OPUS_SHA256=65c1d2f78b9f2fb20082c38cbe47c951ad5839345876e46941612ee87f9a7ce1

RUN apk add --no-cache build-base ca-certificates soxr-dev soxr-static
RUN set -eux; \
    wget -O /tmp/opus.tar.gz \
      "https://github.com/xiph/opus/releases/download/v${OPUS_VERSION}/opus-${OPUS_VERSION}.tar.gz"; \
    echo "${OPUS_SHA256}  /tmp/opus.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/opus.tar.gz -C /tmp; \
    cd "/tmp/opus-${OPUS_VERSION}"; \
    ./configure \
      --prefix=/usr/local \
      --disable-shared \
      --enable-static \
      --disable-extra-programs \
      --disable-doc; \
    make -j"$(getconf _NPROCESSORS_ONLN)"; \
    make install; \
    test -f /usr/local/lib/libopus.a; \
    rm -rf /tmp/opus.tar.gz "/tmp/opus-${OPUS_VERSION}"
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/lib/pkgconfig \
    CGO_ENABLED=1 go build \
    -tags "nolibopusfile netgo osusergo" \
    -trimpath \
    -ldflags '-s -w -linkmode external -extldflags "-static -lgomp"' \
    -o /out/agenrena-rtc-helper \
    ./cmd/agenrena-rtc-helper

FROM scratch AS export
COPY --from=build /out/agenrena-rtc-helper /
