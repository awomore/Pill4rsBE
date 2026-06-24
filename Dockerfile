# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.25.3-alpine AS build

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Build the statically-linked binary.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api

# --- Runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 app

WORKDIR /app

# The binary applies migrations on boot via file://migrations, so ship them.
COPY --from=build /out/api /app/api
COPY --from=build /src/migrations /app/migrations

USER app
EXPOSE 8080

ENTRYPOINT ["/app/api"]
