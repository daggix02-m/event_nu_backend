# Multi-stage build. The Go 1.26.6 tag matches go.mod exactly (CI uses
# go-version-file: go.mod with the same pin).
#
# CGO is required: internal/infrastructure/imaging (media variant worker)
# uses the kolesa-team/go-webp WebP encoder, so libwebp headers must be
# present at build time and the runtime lib (libwebp7) at run time. That
# rules out scratch/distroless-only runners; this runner is Debian bookworm
# slim with only the runtime lib added.
#
# syntax=docker/dockerfile:1
FROM golang:1.26.6-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends libwebp-dev \
    && rm -rf /var/lib/apt/lists/*

ENV CGO_ENABLED=1
WORKDIR /src

# Cache the module graph first; only invalidated when go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /out/api ./cmd/api \
    && go build -o /out/worker ./cmd/worker \
    && go build -o /out/analytics-rollup ./cmd/analytics-rollup \
    && go install github.com/pressly/goose/v3/cmd/goose@v3.27.3

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libwebp7 wget \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /app --shell /usr/sbin/nologin eventnu

COPY --from=build /out/api /app/api
COPY --from=build /out/worker /app/worker
COPY --from=build /out/analytics-rollup /app/analytics-rollup
COPY --from=build /go/bin/goose /app/goose

# The API serves on PORT (default 8080). The docs specify its scope; the
# worker and rollup binaries serve nothing.
WORKDIR /app
USER eventnu

# Config is entirely environment-driven (internal/config); no file secrets
# in the image. Readiness is the app's own /readyz: 200 = Postgres reachable.
# The Compose file overrides CMD with /app/worker (and /app/analytics-rollup
# for one-shot jobs); docker run with no override serves the API.
EXPOSE 8080
CMD ["/app/api"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD PORT=${PORT:-8080} && wget -q -O /dev/null "http://127.0.0.1:$PORT/readyz"
