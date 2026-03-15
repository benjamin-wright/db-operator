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
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/db-operator ./cmd/db-operator

# ─── Runtime Stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot AS runtime

# Copy the compiled binary and always expose it under a fixed name inside the image
COPY --from=builder /out/db-operator /usr/local/bin/db-operator

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/db-operator"]