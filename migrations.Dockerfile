# ─── Build Stage ───────────────────────────────────────────────────────────────
ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /workspace

# Cache module downloads separately from source
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy source and build a static binary
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/db-migrations

# ─── Runtime Stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot AS runtime

# Copy the compiled binary
COPY --from=builder /out/migrate /usr/local/bin/migrate

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/migrate"]
