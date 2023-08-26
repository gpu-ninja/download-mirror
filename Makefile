SRCS := $(shell find . -type f -name '*.go' -not -path "./vendor/*")

bin/download-mirror: $(SRCS)
	@mkdir -p bin
	go build -o $@ cmd/download-mirror/main.go

tidy:
	go mod tidy
	go fmt ./...

lint:
	golangci-lint run ./...

test:
	go test -coverprofile=coverage.out -v ./...

clean:
	-rm -rf bin
	go clean -testcache

.PHONY: tidy lint test clean