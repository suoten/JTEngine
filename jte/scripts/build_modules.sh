#!/bin/bash
set -e

MODULES_DIR="./jte-modules"
OUTPUT_DIR="./dist/modules"
PRIVATE_KEY=${1:-"./keys/module_private.pem"}

echo "=== JTE Module Build & Sign Script ==="

mkdir -p ${OUTPUT_DIR}

build_and_sign() {
    local module_name=$1
    local module_dir="${MODULES_DIR}/${module_name}"

    if [ ! -d "${module_dir}" ]; then
        echo "SKIP: ${module_name} directory not found"
        return
    fi

    echo "Building ${module_name}..."

    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=plugin -o "${OUTPUT_DIR}/${module_name}.so" "./${module_dir}/"

    if [ $? -ne 0 ]; then
        echo "FAILED: ${module_name} build error"
        return
    fi

    echo "Signing ${module_name}..."

    SHA256=$(sha256sum "${OUTPUT_DIR}/${module_name}.so" | awk '{print $1}')
    echo "${SHA256}" > "${OUTPUT_DIR}/${module_name}.sha256"

    if [ -f "${PRIVATE_KEY}" ]; then
        openssl dgst -sha256 -sign "${PRIVATE_KEY}" -out "${OUTPUT_DIR}/${module_name}.sig" "${OUTPUT_DIR}/${module_name}.so"
        echo "SIGNED: ${module_name}"
    else
        echo "UNSIGNED: ${module_name} (no private key)"
    fi
}

build_and_sign "module-storage"
build_and_sign "module-web"
build_and_sign "module-crypto"
build_and_sign "module-adapter"
build_and_sign "module-cluster"
build_and_sign "module-monitor"
build_and_sign "module-regional"
build_and_sign "module-legacy"
build_and_sign "module-ai"
build_and_sign "module-ai-nlp"

echo "=== Module Build Complete ==="
ls -la ${OUTPUT_DIR}/