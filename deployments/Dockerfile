# Stage 1: Build
# Use the official Go image pinned to a specific patch version for reproducibility.
FROM golang:1.22.2-alpine AS builder

# Security: create a non-root user for the final image.
RUN adduser -D -u 1001 gateway

WORKDIR /build

# Download dependencies first (cached as a separate layer — only re-runs if go.mod/go.sum change).
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
# CGO_ENABLED=0: pure Go binary, no dynamic linking, compatible with distroless.
# -ldflags "-s -w": strip debug symbols to reduce binary size.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w" \
    -o /build/gateway \
    ./cmd/gateway

# ─────────────────────────────────────────────────────────────────────────────

# Stage 2: Run
# distroless/static: no shell, no package manager, no OS utilities.
# Attack surface is the binary + its TLS certs only.
FROM gcr.io/distroless/static:nonroot

# Copy the gateway binary from the builder.
COPY --from=builder /build/gateway /gateway

# Copy the example config; operators override this via volume mount or ConfigMap.
COPY --from=builder /build/configs/gateway.yaml /etc/gateway/gateway.yaml

# Use the non-root user created in the build stage.
USER nonroot:nonroot

# Gateway traffic port and Prometheus metrics port.
EXPOSE 8080 9090

ENTRYPOINT ["/gateway", "--config", "/etc/gateway/gateway.yaml"]
