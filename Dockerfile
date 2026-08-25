FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/gantry ./cmd/gantry

FROM scratch
COPY --from=build /out/gantry /gantry
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /zoneinfo.zip
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
VOLUME /config
EXPOSE 8380
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/gantry", "-healthcheck"]
ENTRYPOINT ["/gantry"]
