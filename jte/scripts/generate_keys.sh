#!/bin/bash
set -e

echo "=== JTE Key Generation Script ==="

mkdir -p ./keys

echo "Generating RSA-2048 key pair for module signing..."
openssl genrsa -out ./keys/module_private.pem 2048
openssl rsa -in ./keys/module_private.pem -pubout -out ./keys/module_public.pem

echo ""
echo "Private key: ./keys/module_private.pem (KEEP SECRET!)"
echo "Public key:  ./keys/module_public.pem (embed in JTE engine)"
echo ""
echo "IMPORTANT: The private key must ONLY exist on the build server."
echo "The public key will be embedded in the JTE engine for signature verification."