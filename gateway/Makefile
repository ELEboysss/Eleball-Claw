.PHONY: build build-nocgo run test docker-build clean

APP_NAME=eleball-gateway
BUILD_DIR=./build

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

# 纯 Go 编译（无需 CGO，适合 Windows 开发环境或交叉编译）
build-nocgo:
	CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

run:
	go run ./cmd/server

test:
	go test -v ./...

docker-build:
	docker build -t $(APP_NAME):latest -f deployments/Dockerfile .

clean:
	rm -rf $(BUILD_DIR)
