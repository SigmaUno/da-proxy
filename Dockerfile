FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=1 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o da-proxy ./cmd/da-proxy

FROM alpine:3.21

RUN apk add --no-cache ca-certificates sqlite-libs && \
    mkdir -p /etc/da-proxy /data

COPY --from=builder /build/da-proxy /usr/local/bin/da-proxy
COPY configs/config.example.yaml /etc/da-proxy/config.yaml

EXPOSE 443 8080 9090 9191 26656

ENTRYPOINT ["da-proxy"]
CMD ["-config", "/etc/da-proxy/config.yaml"]
