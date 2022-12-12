TARGET_BIN = object-storage-ui-$(shell git rev-parse --short HEAD)
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