# release.yml passes VERSION explicitly (the pushed tag); locally, fall
# back to `git describe` so a plain `make build` reports something more
# useful than a bare "dev" -- e.g. "v0.1.0-4-gabc1234" a few commits
# past a tag, or the short SHA alone once no tag is reachable at all.
# The final `|| echo dev` only fires outside a git checkout entirely
# (e.g. a source tarball with no .git).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build web release test lint fmt docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o gantry ./cmd/gantry

# web builds the Vite SPA into internal/server/webdist/, which
# webfs_dist.go (-tags webdist) then embeds. Not needed for `build` or
# `test` above -- only `release` and `docker` need a node toolchain.
# `lint` below opportunistically type-checks the -tags webdist build too,
# but only when this has already populated the directory -- it doesn't
# require a node toolchain on its own.
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
	@# webfs_dist.go (-tags webdist) is otherwise never type-checked by a
	@# plain `go vet`/`go test` -- catch it going stale whenever `web` has
	@# already been run at least once (CI always runs `web` first; a bare
	@# local clone that's never run `make web` skips this gracefully,
	@# same as the golangci-lint-not-installed fallback above).
	@if [ -n "$$(ls -A internal/server/webdist 2>/dev/null)" ]; then \
		go build -tags webdist ./...; \
	else \
		echo "internal/server/webdist not populated (run 'make web' first) -- skipping the webdist-tagged build check"; \
	fi
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

docker:
	docker build --build-arg VERSION=$(VERSION) -t gantry:dev .
