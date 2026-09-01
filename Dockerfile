# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.25-alpine
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${NODE_IMAGE} AS web-build
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

FROM ${GO_IMAGE} AS go-build
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . ./
COPY --from=web-build /src/web/dist/ ./internal/webui/dist/

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w \
        -X github.com/hkjang/appstore/internal/buildinfo.Version=${VERSION} \
        -X github.com/hkjang/appstore/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/hkjang/appstore/internal/buildinfo.BuildDate=${BUILD_DATE}" \
      -o /out/appstore ./cmd/server

FROM ${RUNTIME_IMAGE} AS runtime

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="appstore" \
      org.opencontainers.image.description="Offline-ready developer application catalog" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/hkjang/appstore" \
      org.opencontainers.image.licenses="MIT"

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 appstore \
    && adduser -S -D -H -u 10001 -G appstore appstore

COPY --from=go-build --chown=appstore:appstore /out/appstore /usr/local/bin/appstore

USER appstore:appstore
WORKDIR /home/appstore
EXPOSE 8080
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
  CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8080/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/appstore"]
