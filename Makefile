BINARY := svitlobot
# Версія береться з git-тегу (пріоритет), потім коміт; інжектиться у бінарник
# через -X main.version — тому в main.go version має бути var, не const.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build linux linux-arm64 docker docker-arm64 test clean

# Білд під поточну платформу
build:
	go build $(LDFLAGS) -o $(BINARY) .

# Статичний бинарник для LXC (linux/amd64) — без CGO, без залежностей
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o build/svitlobot-linux-amd64 .

linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o build/svitlobot-linux-arm64 .

docker:
	docker build -t svitlobot-client:latest .

# образ для Raspberry Pi 5 (arm64) з будь-якого хоста — без QEMU
docker-arm64:
	docker buildx build --platform linux/arm64 -t svitlobot-client:arm64 --load .

test:
	go test ./...

clean:
	rm -rf build $(BINARY)
