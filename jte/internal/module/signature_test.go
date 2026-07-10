package module

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySignature_ValidSignature(t *testing.T) {
	tmpDir := t.TempDir()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	if err := SetPublicKey(string(pubKeyPEM)); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	moduleData := []byte("fake module binary data for testing")
	modulePath := filepath.Join(tmpDir, "test_module.so")
	if err := os.WriteFile(modulePath, moduleData, 0644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	hash := sha256.Sum256(moduleData)
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	sigPath := filepath.Join(tmpDir, "test_module.so.sig")
	if err := os.WriteFile(sigPath, signature, 0644); err != nil {
		t.Fatalf("write signature: %v", err)
	}

	if err := VerifySignature(modulePath, sigPath); err != nil {
		t.Errorf("VerifySignature should pass for valid signature, got: %v", err)
	}
}

func TestVerifySignature_TamperedModule(t *testing.T) {
	tmpDir := t.TempDir()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pubKeyBytes, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	pubKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})
	SetPublicKey(string(pubKeyPEM))

	moduleData := []byte("original module data")
	modulePath := filepath.Join(tmpDir, "test_module.so")
	os.WriteFile(modulePath, moduleData, 0644)

	hash := sha256.Sum256(moduleData)
	signature, _ := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])

	sigPath := filepath.Join(tmpDir, "test_module.so.sig")
	os.WriteFile(sigPath, signature, 0644)

	tamperedData := []byte("tampered module data!!!!")
	os.WriteFile(modulePath, tamperedData, 0644)

	if err := VerifySignature(modulePath, sigPath); err == nil {
		t.Error("VerifySignature should fail for tampered module")
	}
}

func TestVerifySignature_InvalidSignature(t *testing.T) {
	tmpDir := t.TempDir()

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	pubKeyBytes, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	pubKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})
	SetPublicKey(string(pubKeyPEM))

	modulePath := filepath.Join(tmpDir, "test_module.so")
	os.WriteFile(modulePath, []byte("module data"), 0644)

	sigPath := filepath.Join(tmpDir, "test_module.so.sig")
	os.WriteFile(sigPath, []byte("invalid signature data"), 0644)

	if err := VerifySignature(modulePath, sigPath); err == nil {
		t.Error("VerifySignature should fail for invalid signature")
	}
}