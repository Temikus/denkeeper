package security

import (
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateKeyPair_Succeeds(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("private key size = %d, want %d", len(priv), ed25519.PrivateKeySize)
	}
}

func TestSignAndVerify_ValidSignature(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	data := []byte("hello plugin world")
	sig := Sign(priv, data)

	if !Verify(pub, data, sig) {
		t.Error("Verify returned false for valid signature")
	}
}

func TestVerify_InvalidSignature(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	data := []byte("hello plugin world")
	badSig := make([]byte, ed25519.SignatureSize)

	if Verify(pub, data, badSig) {
		t.Error("Verify returned true for invalid signature")
	}
}

func TestVerify_TamperedData(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	data := []byte("original data")
	sig := Sign(priv, data)

	tampered := []byte("tampered data")
	if Verify(pub, tampered, sig) {
		t.Error("Verify returned true for tampered data")
	}
}

func TestVerifyWithAnyKey_MatchesSecondKey(t *testing.T) {
	pub1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	pub2, priv2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	data := []byte("signed by key 2")
	sig := Sign(priv2, data)

	if !VerifyWithAnyKey([]ed25519.PublicKey{pub1, pub2}, data, sig) {
		t.Error("VerifyWithAnyKey returned false when second key should match")
	}
}

func TestVerifyWithAnyKey_NoMatch(t *testing.T) {
	pub1, _, _ := GenerateKeyPair()
	_, priv2, _ := GenerateKeyPair()

	data := []byte("signed by unknown key")
	sig := Sign(priv2, data)

	if VerifyWithAnyKey([]ed25519.PublicKey{pub1}, data, sig) {
		t.Error("VerifyWithAnyKey returned true when no key should match")
	}
}

func TestMarshalParsePublicKeyPEM_Roundtrip(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pemData := MarshalPublicKeyPEM(pub)
	parsed, err := ParsePublicKeyPEM(pemData)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM: %v", err)
	}

	if !pub.Equal(parsed) {
		t.Error("parsed public key does not match original")
	}
}

func TestMarshalParsePrivateKeyPEM_Roundtrip(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pemData := MarshalPrivateKeyPEM(priv)
	parsed, err := ParsePrivateKeyPEM(pemData)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM: %v", err)
	}

	if !priv.Equal(parsed) {
		t.Error("parsed private key does not match original")
	}
}

func TestParsePublicKeyPEM_BadPEMType(t *testing.T) {
	_, priv, _ := GenerateKeyPair()
	// Accidentally pass a private key PEM as a public key.
	pemData := MarshalPrivateKeyPEM(priv)
	_, err := ParsePublicKeyPEM(pemData)
	if err == nil {
		t.Fatal("expected error for wrong PEM type, got nil")
	}
}

func TestParsePublicKeyPEM_NoPEMBlock(t *testing.T) {
	_, err := ParsePublicKeyPEM([]byte("not a pem file"))
	if err == nil {
		t.Fatal("expected error for non-PEM data, got nil")
	}
}

func TestLoadTrustedKeys_FileNotFound(t *testing.T) {
	_, err := LoadTrustedKeys([]string{"/nonexistent/key.pub"})
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadTrustedKeys_ValidFile(t *testing.T) {
	pub, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.pub")
	if err := os.WriteFile(keyPath, MarshalPublicKeyPEM(pub), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	keys, err := LoadTrustedKeys([]string{keyPath})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if !keys[0].Equal(pub) {
		t.Error("loaded key does not match original")
	}
}

func TestSignAndVerifyFile_Roundtrip(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "my-plugin")
	if err := os.WriteFile(pluginPath, []byte("plugin binary content"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SignFile(priv, pluginPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	// Verify the signature file was created.
	sigPath := pluginPath + SignatureFileExtension
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("signature file not created: %v", err)
	}

	if err := VerifyFile([]ed25519.PublicKey{pub}, pluginPath); err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
}

func TestVerifyFile_MissingSignature(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "unsigned-plugin")
	if err := os.WriteFile(pluginPath, []byte("plugin binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := VerifyFile([]ed25519.PublicKey{pub}, pluginPath)
	if err == nil {
		t.Fatal("expected error for missing signature, got nil")
	}
}

func TestVerifyFile_TamperedContent(t *testing.T) {
	pub, priv, _ := GenerateKeyPair()
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "tampered-plugin")
	if err := os.WriteFile(pluginPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := SignFile(priv, pluginPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	// Tamper with the file after signing.
	if err := os.WriteFile(pluginPath, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := VerifyFile([]ed25519.PublicKey{pub}, pluginPath)
	if err == nil {
		t.Fatal("expected error for tampered file, got nil")
	}
}

func TestLoadTrustedKeys_MultipleFiles(t *testing.T) {
	pub1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	pub2, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	dir := t.TempDir()
	path1 := filepath.Join(dir, "key1.pub")
	path2 := filepath.Join(dir, "key2.pub")
	if err := os.WriteFile(path1, MarshalPublicKeyPEM(pub1), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(path2, MarshalPublicKeyPEM(pub2), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	keys, err := LoadTrustedKeys([]string{path1, path2})
	if err != nil {
		t.Fatalf("LoadTrustedKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if !keys[0].Equal(pub1) {
		t.Error("first loaded key does not match pub1")
	}
	if !keys[1].Equal(pub2) {
		t.Error("second loaded key does not match pub2")
	}
}

func TestVerifyFile_CorruptedSignature(t *testing.T) {
	pub, priv, _ := GenerateKeyPair()
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(pluginPath, []byte("plugin binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := SignFile(priv, pluginPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	// Truncate the .sig file to corrupt it.
	sigPath := pluginPath + SignatureFileExtension
	if err := os.WriteFile(sigPath, []byte("short"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := VerifyFile([]ed25519.PublicKey{pub}, pluginPath)
	if err == nil {
		t.Fatal("expected error for corrupted signature, got nil")
	}
}

func TestParsePublicKeyPEM_WrongSize(t *testing.T) {
	// Create a PEM block with the right type but wrong key size.
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  PEMTypePublicKey,
		Bytes: []byte("too-short"),
	})
	_, err := ParsePublicKeyPEM(pemData)
	if err == nil {
		t.Fatal("expected error for wrong key size, got nil")
	}
}

func TestParsePrivateKeyPEM_WrongSize(t *testing.T) {
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  PEMTypePrivateKey,
		Bytes: []byte("too-short"),
	})
	_, err := ParsePrivateKeyPEM(pemData)
	if err == nil {
		t.Fatal("expected error for wrong key size, got nil")
	}
}

func TestParsePrivateKeyPEM_WrongPEMType(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	pemData := MarshalPublicKeyPEM(pub)
	_, err := ParsePrivateKeyPEM(pemData)
	if err == nil {
		t.Fatal("expected error for wrong PEM type, got nil")
	}
}

func TestParsePrivateKeyPEM_NoPEMBlock(t *testing.T) {
	_, err := ParsePrivateKeyPEM([]byte("not a pem file"))
	if err == nil {
		t.Fatal("expected error for non-PEM data, got nil")
	}
}

func TestSignFile_FileNotFound(t *testing.T) {
	_, priv, _ := GenerateKeyPair()
	err := SignFile(priv, "/nonexistent/path/plugin")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestVerifyFile_FileNotFound(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	err := VerifyFile([]ed25519.PublicKey{pub}, "/nonexistent/path/plugin")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestVerifyFile_EmptyTrustedKeys(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(pluginPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, priv, _ := GenerateKeyPair()
	if err := SignFile(priv, pluginPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	err := VerifyFile([]ed25519.PublicKey{}, pluginPath)
	if err == nil {
		t.Fatal("expected error when no trusted keys provided, got nil")
	}
}

func TestLoadTrustedKeys_EmptyPaths(t *testing.T) {
	keys, err := LoadTrustedKeys([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestLoadTrustedKeys_MixedValidInvalid(t *testing.T) {
	pub, _, _ := GenerateKeyPair()
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.pub")
	if err := os.WriteFile(validPath, MarshalPublicKeyPEM(pub), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadTrustedKeys([]string{validPath, "/nonexistent/key.pub"})
	if err == nil {
		t.Fatal("expected error when second path is invalid, got nil")
	}
}
