TARGET_BIN = object-storage-ui
TARGET_ARCH = amd64
SOURCE_MAIN = main.go

all: build

build: build-darwin build-linux build-windows

build-darwin:
	GOOS=darwin GOARCH=$(TARGET_ARCH) go build -o bin/$(TARGET_BIN)_darwin-universal $(SOURCE_MAIN)

build-linux:
	GOOS=linux GOARCH=$(TARGET_ARCH) go build -o bin/$(TARGET_BIN)_linux-amd64 $(SOURCE_MAIN)

build-windows:
	GOOS=windows GOARCH=$(TARGET_ARCH) go build -o bin/$(TARGET_BIN)_windows-amd64.exe $(SOURCE_MAIN)

start:
	go run $(SOURCE_MAIN)

SHA = $(shell git rev-parse --short HEAD)
DOCKER_IMAGE = robbymilo/object-storage-ui:$(SHA)

docker-build:
	docker build --platform linux/x86_64 -t $(DOCKER_IMAGE) .

docker-run:
	docker run \
		--platform linux/x86_64 \
		-v ~/key.json:/key.json \
		-e GOOGLE_APPLICATION_CREDENTIALS=/key.json \
		-p 3000:3000 \
		--rm -it \
		$(DOCKER_IMAGE) \
		--bucket-name staging-static-grafana-com