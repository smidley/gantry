FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# outDir in vite.config.ts is '../internal/server/webdist' -- relative to
# this stage's WORKDIR, that lands at /src/internal/server/webdist, which
# the go-build stage below copies in by that same absolute path.
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/internal/server/webdist ./internal/server/webdist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -tags webdist -ldflags "-s -w -X main.version=${VERSION}" -o /out/gantry ./cmd/gantry

# The runtime image. gantry is one static (CGO_ENABLED=0) binary and would
# run on scratch, but scratch has no dynamic loader -- so the glibc
# nvidia-smi that `--runtime=nvidia` injects at runtime can't exec there
# (issue #38): its ELF interpreter, /lib64/ld-linux-x86-64.so.2, is absent,
# and every exec of it fails ENOENT. distroless/base-debian12 is a complete
# glibc/Debian userland WITH that loader, so the mounted nvidia-smi and
# driver libraries have an interpreter to run under, while Intel/AMD (which
# read DRM fdinfo in-process, no child exec) and CPU-only boxes are
# unaffected. It keeps the properties scratch gave us -- no shell, no
# package manager, minimal attack surface -- and still ships CA certs and
# tzdata; the trade is a larger image (~14 MB grows to ~35 MB) for one
# image that serves every GPU vendor.
#
# A musl base (alpine) can't take this role: its loader is
# /lib/ld-musl-x86_64.so.1 and musl is not glibc ABI-compatible, so a
# glibc nvidia-smi fails the same ENOENT there as on scratch. A
# hand-assembled loader+libc-into-scratch bundle would be smaller still,
# but a partial glibc is fragile exactly where we can't test it -- on real
# Nvidia hardware the container toolkit expects a normal glibc layout it
# can ldconfig against -- so a complete, coherent userland wins over a few
# saved MB.
FROM gcr.io/distroless/base-debian12
COPY --from=build /out/gantry /gantry
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
VOLUME /config
EXPOSE 8380
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/gantry", "-healthcheck"]
ENTRYPOINT ["/gantry"]
