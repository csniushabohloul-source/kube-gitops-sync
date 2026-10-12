# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code
COPY . .

# Compile statically-linked binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/akit-ops-lite .

# Production stage
FROM alpine:3.19 AS production

# Install runtime dependencies (certificates for HTTPS git, git cli if needed)
RUN apk add --no-cache ca-certificates git tzdata \
    && addgroup -g 10001 appgroup \
    && adduser -u 10001 -G appgroup -s /bin/sh -D appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/akit-ops-lite /app/akit-ops-lite

# Create workspace directory with correct permissions
RUN mkdir -p /app/deployrepo && chown -R appuser:appgroup /app

# Run as non-root user
USER appuser

ENTRYPOINT ["/app/akit-ops-lite"]