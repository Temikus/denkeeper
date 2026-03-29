package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/security"
)

func TestRunPluginKeygen_CreatesKeyPair(t *testing.T) {
	dir := t.TempDir()

	if err := runPluginKeygen("test", dir); err != nil {
		t.Fatalf("runPluginKeygen: %v", err)
	}

	pubPath := filepath.Join(dir, "test.pub")
	keyPath := filepath.Join(dir, "test.key")

	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatalf("reading public key: %v", err)
	}
	if _, err := security.ParsePublicKeyPEM(pubData); err != nil {
		t.Errorf("invalid public key PEM: %v", err)
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading private key: %v", err)
	}
	if _, err := security.ParsePrivateKeyPEM(keyData); err != nil {
		t.Errorf("invalid private key PEM: %v", err)
	}

	// Private key should have restricted permissions.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key permissions = %o, want 0600", perm)
	}
}

func TestRunPluginKeygen_BadOutputDir(t *testing.T) {
	err := runPluginKeygen("test", "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent output directory")
	}
}

func TestRunPluginSign_Success(t *testing.T) {
	dir := t.TempDir()

	// Generate a key pair.
	pub, priv, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	keyPath := filepath.Join(dir, "test.key")
	if err := os.WriteFile(keyPath, security.MarshalPrivateKeyPEM(priv), 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}

	// Create a fake binary.
	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	if err := runPluginSign(binPath, keyPath); err != nil {
		t.Fatalf("runPluginSign: %v", err)
	}

	// Verify the signature is valid.
	sigPath := binPath + security.SignatureFileExtension
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("signature file not created: %v", err)
	}

	if err := security.VerifyFile([]ed25519.PublicKey{pub}, binPath); err != nil {
		t.Errorf("VerifyFile failed: %v", err)
	}
}

func TestRunPluginSign_BadKeyPath(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := runPluginSign(binPath, "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestRunPluginSign_BadBinaryPath(t *testing.T) {
	dir := t.TempDir()

	_, priv, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	keyPath := filepath.Join(dir, "test.key")
	if err := os.WriteFile(keyPath, security.MarshalPrivateKeyPEM(priv), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err = runPluginSign("/nonexistent/binary", keyPath)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestRunPluginVerify_Success(t *testing.T) {
	dir := t.TempDir()

	pub, priv, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pubPath := filepath.Join(dir, "test.pub")
	if err := os.WriteFile(pubPath, security.MarshalPublicKeyPEM(pub), 0o644); err != nil {
		t.Fatalf("WriteFile pub: %v", err)
	}

	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile binary: %v", err)
	}

	if err := security.SignFile(priv, binPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	if err := runPluginVerify(binPath, []string{pubPath}); err != nil {
		t.Errorf("runPluginVerify: %v", err)
	}
}

func TestRunPluginVerify_BadSignature(t *testing.T) {
	dir := t.TempDir()

	// Generate one key pair for signing, another for verification.
	_, priv, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	otherPub, _, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	otherPubPath := filepath.Join(dir, "other.pub")
	if err := os.WriteFile(otherPubPath, security.MarshalPublicKeyPEM(otherPub), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := security.SignFile(priv, binPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	err = runPluginVerify(binPath, []string{otherPubPath})
	if err == nil {
		t.Fatal("expected verification failure with wrong key")
	}
}

func TestRunPluginVerify_MissingSignatureFile(t *testing.T) {
	dir := t.TempDir()

	pub, _, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pubPath := filepath.Join(dir, "test.pub")
	if err := os.WriteFile(pubPath, security.MarshalPublicKeyPEM(pub), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err = runPluginVerify(binPath, []string{pubPath})
	if err == nil {
		t.Fatal("expected error for missing .sig file")
	}
}

func TestRunPluginKeygen_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	// keygen → sign → verify pipeline.
	if err := runPluginKeygen("mykey", dir); err != nil {
		t.Fatalf("keygen: %v", err)
	}

	binPath := filepath.Join(dir, "myplugin")
	if err := os.WriteFile(binPath, []byte("plugin binary data"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	keyPath := filepath.Join(dir, "mykey.key")
	if err := runPluginSign(binPath, keyPath); err != nil {
		t.Fatalf("sign: %v", err)
	}

	pubPath := filepath.Join(dir, "mykey.pub")
	if err := runPluginVerify(binPath, []string{pubPath}); err != nil {
		t.Errorf("verify: %v", err)
	}
}
