VERSION ?= dev

.PHONY: build test lint fmt docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o gantry ./cmd/gantry

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

docker:
	docker build --build-arg VERSION=$(VERSION) -t gantry:dev .
