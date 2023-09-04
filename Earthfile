VERSION 0.7
FROM golang:1.20-bookworm
WORKDIR /app

docker-all:
  BUILD --platform=linux/amd64 --platform=linux/arm64 +docker

docker:
  ARG TARGETPLATFORM
  ARG VERSION
  FROM debian:bookworm-slim
  RUN apt update \
    && apt install -y ca-certificates
  COPY LICENSE /usr/local/share/download-mirror/
  COPY +download-mirror/download-mirror /usr/local/bin/
  EXPOSE 8080/tcp
  EXPOSE 8443/tcp
  ENTRYPOINT ["/usr/local/bin/download-mirror"]
  SAVE IMAGE --push ghcr.io/gpu-ninja/download-mirror:${VERSION}
  SAVE IMAGE --push ghcr.io/gpu-ninja/download-mirror:latest

download-mirror:
  ARG GOOS=linux
  ARG GOARCH=amd64
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN CGO_ENABLED=0 go build --ldflags '-s' -o download-mirror cmd/download-mirror/main.go
  SAVE ARTIFACT ./download-mirror AS LOCAL dist/download-mirror-${GOOS}-${GOARCH}

tidy:
  LOCALLY
  RUN go mod tidy
  RUN go fmt ./...

lint:
  FROM golangci/golangci-lint:v1.54.2
  WORKDIR /app
  COPY . ./
  RUN golangci-lint run --timeout 5m ./...

test:
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN go test -coverprofile=coverage.out -v ./...
  SAVE ARTIFACT ./coverage.out AS LOCAL coverage.out