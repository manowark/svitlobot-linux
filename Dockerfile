# syntax=docker/dockerfile:1

# ---------- стадія білда ----------
# BUILDPLATFORM — архітектура машини, де йде білд; TARGETOS/TARGETARCH
# підставляє buildx для таргет-платформи. CGO вимкнено, тому Go
# крос-компілює нативно: білд linux/arm64 (Raspberry Pi 5) працює БЕЗ
# QEMU-емуляції навіть з amd64-хоста.
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# VERSION — тег релізу, передається з GitHub Actions (build-args) або
# локально: docker build --build-arg VERSION=1.2.3 .
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/svitlobot .

# ---------- runtime ----------
# alpine (не scratch): потрібні ca-certificates для HTTPS до
# api.svitlobot.in.ua і tzdata для локального часу в логах.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /etc/svitlobot

COPY --from=build /out/svitlobot /usr/local/bin/svitlobot

# Конфікг монтується ззовні (ro) через docker-compose/volumes.
# Процес працює від root — це потрібно для ICMP raw-сокета (капа NET_RAW
# видається через cap_add, див. docker-compose.yml). Для type: tcp
# привілеї не потрібні.
WORKDIR /etc/svitlobot

ENTRYPOINT ["/usr/local/bin/svitlobot"]
CMD ["-config", "/etc/svitlobot/config.json"]

# docker stop / compose stop шле SIGTERM — клієнт шатдауниться коректно.
STOPSIGNAL SIGTERM
