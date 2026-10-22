# Build stage
# Pinned to a patched Go (matches CI; fixes the stdlib CVEs govulncheck found —
# RFC D2) instead of the drifted golang:1.23. Ideal hardening is to pin by
# digest (FROM golang:1.27.3-alpine@sha256:...); the exact digest is added when
# built in an environment with registry access.
FROM golang:1.27.3-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev sqlite-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o cainban ./cmd/cainban

# Final stage
# Pinned to a concrete Alpine release (was alpine:latest, an unpinned moving
# target). Pin by digest when built with registry access.
FROM alpine:3.21

# Install runtime dependencies
RUN apk --no-cache add ca-certificates sqlite

# Run as a non-root user (RFC M8/C3): create an unprivileged user + its home,
# own the data dir, and drop to it. The container no longer runs as root.
RUN addgroup -S cainban && adduser -S -G cainban -h /home/cainban cainban

WORKDIR /home/cainban

# Copy the binary from builder stage
COPY --from=builder /app/cainban .

# Create data directory owned by the non-root user
RUN mkdir -p /home/cainban/.cainban && chown -R cainban:cainban /home/cainban

USER cainban

# Expose any necessary ports (if needed in future)
# EXPOSE 8080

CMD ["./cainban"]
