# JTE — JT/T 部标协议引擎（开源版）
# Makefile — 常用开发命令

GO ?= go
GOFLAGS ?= -count=1
LDFLAGS := -s -w
BINARY := jte

.PHONY: all build build-binary test test-coverage vet lint benchmark clean fmt imports docker-build docker-run acceptance help

## all: 构建并测试（默认目标）
all: build test

## build: 编译所有包
build:
	$(GO) build ./...

## build-binary: 编译可执行文件
build-binary:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/jte

## test: 运行所有测试
test:
	$(GO) test $(GOFLAGS) -timeout 180s ./...

## test-coverage: 运行测试并生成覆盖率报告
test-coverage:
	$(GO) test $(GOFLAGS) -timeout 180s -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## vet: 运行 go vet
vet:
	$(GO) vet ./...

## lint: 运行 golangci-lint（需安装 golangci-lint）
lint:
	@if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run ./... ; \
	else \
		echo "golangci-lint not installed, running go vet instead" ; \
		$(GO) vet ./... ; \
	fi

## benchmark: 运行基准测试
benchmark:
	$(GO) test -bench=. -benchmem -timeout 300s ./pkg/protocol/... ./internal/...

## fmt: 格式化代码
fmt:
	$(GO) fmt ./...

## imports: 整理 import
imports:
	@if command -v goimports > /dev/null 2>&1; then \
		goimports -w . ; \
	else \
		echo "goimports not installed, skipping" ; \
	fi

## clean: 清理构建产物
clean:
	$(GO) clean -cache
	rm -f bin/$(BINARY) coverage.out coverage.html

## docker-build: 构建 Docker 镜像
docker-build:
	docker build -t jte-engine:latest -f deploy/Dockerfile .

## docker-run: 运行 Docker 容器
docker-run:
	docker run -d --name jte -p 8080:8080 -p 7611:7611 jte-engine:latest

## acceptance: 运行端到端验收脚本
acceptance:
	@if [ -f ../scripts/acceptance_e2e.sh ]; then \
		bash ../scripts/acceptance_e2e.sh ; \
	else \
		echo "验收脚本不存在，请检查 scripts/acceptance_e2e.sh" ; \
		exit 1 ; \
	fi

## help: 显示帮助
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //g' | awk 'BEGIN {FS = ": "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := all
