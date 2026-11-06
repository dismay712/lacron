# syntax=docker/dockerfile:1
# Build with --pull to resolve the latest stable Alpine and latest patched LTS.
ARG ALPINE_IMAGE=alpine:latest
ARG NODE_IMAGE=node:lts-alpine
ARG GO_IMAGE=golang:alpine

FROM ${NODE_IMAGE} AS node
USER 0:0
RUN apk add --no-cache binutils \
    && mkdir -p /out \
    && strip --strip-unneeded -o /out/node /usr/local/bin/node

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X github.com/dismay712/lacron/internal/lacron.Version=${VERSION}" \
    -o /out/lacron ./cmd/lacron

FROM ${ALPINE_IMAGE} AS runtime
LABEL org.opencontainers.image.title="lacron" \
      org.opencontainers.image.description="Second-resolution cron with a Go WebUI, Node.js LTS and Shell scripts" \
      org.opencontainers.image.source="https://github.com/dismay712/lacron" \
      org.opencontainers.image.licenses="MIT"
ARG EXTRA_APK_PACKAGES=""
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates libatomic libstdc++ tini tzdata ${EXTRA_APK_PACKAGES} \
    && addgroup -g 10001 lacron \
    && adduser -D -H -u 10001 -G lacron -s /sbin/nologin lacron \
    && mkdir -p /data /scripts /run/lacron \
    && chmod 700 /data /run/lacron
# Copy the runtime and license separately so build tools and headers never
# enter the final image layers.
COPY --from=node /out/node /usr/local/bin/node
COPY --from=node /usr/local/LICENSE /usr/local/LICENSE
RUN ln -s node /usr/local/bin/nodejs \
    && command -v crond \
    && node -e 'if (!process.release.lts) throw new Error("Node.js must be an LTS release"); console.log(process.version, process.release.lts)'
COPY --from=build /out/lacron /usr/local/bin/lacron
COPY docker/entrypoint.sh /usr/local/bin/lacron-entrypoint
RUN chmod 755 /usr/local/bin/lacron-entrypoint
COPY scripts/ /scripts/
COPY LICENSE /usr/share/licenses/lacron/LICENSE
ENV TZ=UTC LACRON_ADDR=:8080 LACRON_DATA_DIR=/data LACRON_SOCKET=/run/lacron/runner.sock
WORKDIR /scripts
USER 0:0
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 CMD ["/usr/local/bin/lacron", "healthcheck"]
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/lacron-entrypoint"]
CMD ["serve"]

# The default target adds npm and its shipped dependencies. Scripts that only
# use Node built-ins can build --target runtime to omit this layer.
FROM runtime AS standard
COPY --from=node /usr/local/lib/node_modules/npm/ /usr/local/lib/node_modules/npm/
RUN set -eu; \
    ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm; \
    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx; \
    npm_config_cache=$(mktemp -d); \
    export npm_config_cache; \
    npm --version; \
    npx --version; \
    rm -rf "$npm_config_cache"
