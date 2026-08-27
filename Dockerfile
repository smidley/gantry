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

FROM scratch
COPY --from=build /out/gantry /gantry
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
VOLUME /config
EXPOSE 8380
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/gantry", "-healthcheck"]
ENTRYPOINT ["/gantry"]
