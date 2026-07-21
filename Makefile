# Eleball-claw 构建/打包
#
# claw 仓库自带 gateway/（上游 Eleball gateway 的裁剪 fork），cmd/claw-server 为本地网关入口。
# 详见本仓库 README 或主仓库 docs/marketing/claw-implementation-plan.md。
#
# 用法：
#   make build          # 编译 claw-server 单文件二进制（CGO_ENABLED=0，跨架构）
#   make build-web      # 构建 web + admin-web 的 dist（claw 本地化 env）
#   make package        # 打包二进制 + 配置 + 前端 dist + 预置模块为分发物
#   make run            # 本地运行 claw-server
#   make clean
#
# GO 工具链：submodule 嵌入主仓库时默认用主仓库 .tools/go；独立 clone 时用 GO=go 覆盖。

GO ?= ../.tools/go/bin/go
GATEWAY := ./gateway
DIST := dist
BINARY := $(DIST)/eleball-claw

# 跨架构目标（默认当前架构；arm64 时显式 GOARCH=arm64）
CGO ?= 0

.PHONY: build build-web package run clean test

build:
	@mkdir -p $(DIST)
	cd $(GATEWAY) && CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build -trimpath -ldflags "-s -w" -o ../$(BINARY) ./cmd/claw-server
	@echo "==> $(BINARY)"

build-web:
	cd $(GATEWAY)/web && VITE_API_BASE=/api VITE_CLOUD_BASE=https://www.eleball.cn VITE_CLOUD_API=https://api.eleball.cn/v1 npm run build
	cd $(GATEWAY)/admin-web && VITE_API_BASE=/api VITE_CLAW_CONSOLE=true npm run build

package: build build-web
	@mkdir -p $(DIST)/pkg
	cp $(BINARY) $(DIST)/pkg/
	cp $(GATEWAY)/configs/claw.yaml $(DIST)/pkg/claw.yaml
	cp -r $(GATEWAY)/marketplace $(DIST)/pkg/marketplace
	@echo "==> 分发物在 $(DIST)/pkg/（二进制 + claw.yaml + 预置模块）"

run:
	cd $(GATEWAY) && $(GO) run ./cmd/claw-server --port=8090

clean:
	rm -rf $(DIST)

test:
	cd $(GATEWAY) && $(GO) test ./internal/...
