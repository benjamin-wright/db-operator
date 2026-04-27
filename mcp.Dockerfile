# ─── Build Stage ───────────────────────────────────────────────────────────────
ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o /out/db-mcp ./cmd/db-mcp

# ─── Runtime Stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot AS runtime

COPY --from=builder /out/db-mcp /usr/local/bin/db-mcp

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/db-mcp"]
