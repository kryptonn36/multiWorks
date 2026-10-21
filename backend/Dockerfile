
# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

# Runtime stage
FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/server ./server
COPY --from=builder /app/sql/migrations ./migrations

RUN mkdir -p /data/assets && chown app:app /data/assets
ENV ASSET_STORAGE_ROOT=/data/assets

USER app

EXPOSE 8080

CMD ["./server"]
