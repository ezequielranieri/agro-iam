# syntax=docker/dockerfile:1

# agro-iam deploy image (Render). Multi-stage:
#   builder  -> compile API + seed binaries (static, CGO off)
#   migrate  -> reuse the official golang-migrate image for its binary
#   runtime  -> minimal alpine + CA certs for Postgres TLS

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agro-iam ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agro-iam-seed ./cmd/seed

FROM migrate/migrate:latest AS migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates

COPY --from=builder /out/agro-iam /usr/local/bin/agro-iam
COPY --from=builder /out/agro-iam-seed /usr/local/bin/agro-iam-seed
COPY --from=migrate /migrate /usr/local/bin/migrate
COPY migrations /migrations
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENV HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]