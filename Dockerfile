# Build stage: the vendored libgadu (static), then the Go binary with cgo.
FROM golang:1.27-trixie AS build
RUN apt-get update \
 && apt-get install -y --no-install-recommends cmake libgnutls28-dev zlib1g-dev pkg-config \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY third_party ./third_party
COPY scripts/build-libgadu.sh ./scripts/
RUN scripts/build-libgadu.sh /opt/libgadu
ENV PKG_CONFIG_PATH=/opt/libgadu/lib/pkgconfig
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TAG=unknown
ARG COMMIT=unknown
RUN go build -tags goolm -trimpath \
      -ldflags "-s -w -X main.Tag=${TAG} -X main.Commit=${COMMIT} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/ ./cmd/...

# Runtime stage: the binary and the shared libraries that libgadu needs.
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends libgnutls30t64 zlib1g ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 1337 bridge \
 && useradd --system --uid 1337 --gid 1337 --home-dir /data --no-create-home bridge \
 && mkdir /data && chown bridge:bridge /data
COPY --from=build /out/matrix-gadu /out/gadu-spike /usr/bin/
COPY docker-run.sh /docker-run.sh
USER 1337
WORKDIR /data
VOLUME /data
CMD ["/docker-run.sh"]
