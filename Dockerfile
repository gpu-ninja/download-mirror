FROM golang:1.20-buster AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 make

FROM debian:bookworm-slim

COPY LICENSE /usr/local/share/url-shortener/
COPY --from=builder /app/bin/url-shortener /usr/local/bin/

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/url-shortener"]