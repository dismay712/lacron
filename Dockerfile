# syntax=docker/dockerfile:1
# Build with --pull to resolve the latest stable Alpine and latest patched LTS.
ARG ALPINE_IMAGE=alpine:latest
ARG NODE_IMAGE=node:lts-alpine
ARG GO_IMAGE=golang:alpine

FROM ${NODE_IMAGE} AS node

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X github.com/dismay712/lacron/internal/lacron.Version=${VERSION}" \
    -o /out/lacron ./cmd/lacron

FROM ${ALPINE_IMAGE}
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
# Preserve the official installation, npm/npx links, and bundled license notices.
COPY --from=node /usr/local/ /usr/local/
RUN rm -f /usr/local/bin/yarn /usr/local/bin/yarnpkg \
    && command -v crond \
    && node -e 'if (!process.release.lts) throw new Error("Node.js must be an LTS release"); console.log(process.version, process.release.lts)' \
    && npm --version
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
