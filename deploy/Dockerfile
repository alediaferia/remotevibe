# The control plane. Multi-stage so a VPS needs neither Go nor a build step —
# `docker compose up -d --build` is the whole install.
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/remotevibed ./cmd/remotevibed

FROM alpine:3.20

# The daemon shells out to `docker` to manage sibling containers; it talks to
# the host daemon through the mounted socket, so only the client is needed.
RUN apk add --no-cache docker-cli ca-certificates

COPY --from=build /out/remotevibed /usr/local/bin/remotevibed

EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8787/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/remotevibed"]
