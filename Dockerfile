FROM golang:1.20-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 make

FROM debian:bookworm-slim

RUN apt update \
  && apt install -y ca-certificates

COPY LICENSE /usr/local/share/download-mirror/
COPY --from=builder /app/bin/download-mirror /usr/local/bin/

EXPOSE 8080/tcp
EXPOSE 8443/tcp
ENTRYPOINT ["/usr/local/bin/download-mirror"]