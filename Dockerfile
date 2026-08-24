# syntax=docker/dockerfile:1.7

# Build stage — CGO-free, statically linkable
FROM golang:1.26-alpine AS builder
WORKDIR /src

ARG VERSION=docker
ARG COMMIT=none
ARG BUILD_DATE=unknown

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/relayscope ./cmd/relayscope

# Runtime stage — minimal, no shell in final image footprint
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S relayscope && adduser -S -G relayscope relayscope

WORKDIR /app
COPY --from=builder /out/relayscope /app/relayscope

# Data directory for SQLite + admin password. Mount a volume here for persistence.
RUN mkdir -p /app/data && chown -R relayscope:relayscope /app
USER relayscope

ENV RELAYSCOPE_LISTEN_ADDR=:8080
ENV RELAYSCOPE_DATA_DIR=/app/data
EXPOSE 8080

# Optional: link a FlareSolverr sidecar via RELAYSCOPE_FLARESOLVERR_ENDPOINT.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health/ready || exit 1

ENTRYPOINT ["/app/relayscope"]
