# ---- build ----
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Copy the module files first so this layer (and `go mod download`) is cached
# across code changes -- rebuilds after editing Go files take seconds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/wallet-api ./cmd/api

# ---- run ----
FROM alpine:3.20

# ca-certificates for outbound TLS, tzdata so TimeZone=Asia/Bangkok resolves.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 appuser

WORKDIR /app
COPY --from=builder /out/wallet-api .

USER appuser
EXPOSE 8080
ENTRYPOINT ["/app/wallet-api"]
