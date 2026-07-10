#!/bin/bash
set -e

VERSION=${1:-1.0.0}
OUTPUT_DIR="./dist"

echo "=== JTE Build Script v${VERSION} ==="

mkdir -p ${OUTPUT_DIR}

echo "[1/4] Building for linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o ${OUTPUT_DIR}/jte-linux-amd64 ./cmd/jte/

echo "[2/4] Building for linux/arm64 (Kylin V10)..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o ${OUTPUT_DIR}/jte-linux-arm64 ./cmd/jte/

echo "[3/4] Building for windows/amd64..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o ${OUTPUT_DIR}/jte-windows-amd64.exe ./cmd/jte/

echo "[4/4] Building for darwin/arm64..."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.Version=${VERSION}" -o ${OUTPUT_DIR}/jte-darwin-arm64 ./cmd/jte/

echo "=== Build Complete ==="
ls -la ${OUTPUT_DIR}/