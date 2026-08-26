VERSION ?= dev

.PHONY: build web release test lint fmt docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o gantry ./cmd/gantry

# web builds the Vite SPA into internal/server/webdist/, which
# webfs_dist.go (-tags webdist) then embeds. Not needed for `build` or
# `test` above -- only `release` and `docker` need a node toolchain.
web:
	cd web && npm ci && npm run build

# release builds the real app shell into internal/server/webdist/ first,
# then compiles the binary with that directory embedded (-tags webdist).
release: web
	CGO_ENABLED=0 go build -trimpath -tags webdist -ldflags "-s -w -X main.version=$(VERSION)" -o gantry ./cmd/gantry

test:
	go test ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, falling back to go vet"; \
		go vet ./...; \
	fi
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

docker:
	docker build --build-arg VERSION=$(VERSION) -t gantry:dev .
