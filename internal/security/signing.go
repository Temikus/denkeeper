package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
)

// SignatureFileExtension is the file extension for detached Ed25519 signatures.
const SignatureFileExtension = ".sig"

// PEMTypePublicKey is the PEM block type for Ed25519 public keys.
const PEMTypePublicKey = "DENKEEPER ED25519 PUBLIC KEY"

// PEMTypePrivateKey is the PEM block type for Ed25519 private keys.
const PEMTypePrivateKey = "DENKEEPER ED25519 PRIVATE KEY"

// GenerateKeyPair creates a new Ed25519 key pair.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generating Ed25519 key pair: %w", err)
	}
	return pub, priv, nil
}

// Sign produces an Ed25519 signature over the given data.
func Sign(privateKey ed25519.PrivateKey, data []byte) []byte {
	return ed25519.Sign(privateKey, data)
}

// Verify checks an Ed25519 signature against the given data and public key.
func Verify(publicKey ed25519.PublicKey, data, signature []byte) bool {
	return ed25519.Verify(publicKey, data, signature)
}

// VerifyWithAnyKey checks a signature against multiple trusted public keys.
// Returns true if any key verifies the signature.
func VerifyWithAnyKey(trustedKeys []ed25519.PublicKey, data, signature []byte) bool {
	for _, key := range trustedKeys {
		if ed25519.Verify(key, data, signature) {
			return true
		}
	}
	return false
}

// MarshalPublicKeyPEM encodes an Ed25519 public key as PEM.
func MarshalPublicKeyPEM(pub ed25519.PublicKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  PEMTypePublicKey,
		Bytes: []byte(pub),
	})
}

// MarshalPrivateKeyPEM encodes an Ed25519 private key as PEM.
func MarshalPrivateKeyPEM(priv ed25519.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  PEMTypePrivateKey,
		Bytes: []byte(priv),
	})
}

// ParsePublicKeyPEM decodes a PEM-encoded Ed25519 public key.
func ParsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != PEMTypePublicKey {
		return nil, fmt.Errorf("unexpected PEM type %q, expected %q", block.Type, PEMTypePublicKey)
	}
	if len(block.Bytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: got %d bytes, expected %d", len(block.Bytes), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(block.Bytes), nil
}

// ParsePrivateKeyPEM decodes a PEM-encoded Ed25519 private key.
func ParsePrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != PEMTypePrivateKey {
		return nil, fmt.Errorf("unexpected PEM type %q, expected %q", block.Type, PEMTypePrivateKey)
	}
	if len(block.Bytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: got %d bytes, expected %d", len(block.Bytes), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(block.Bytes), nil
}

// LoadTrustedKeys reads a list of PEM public key files and returns the parsed keys.
// Returns an error if any file cannot be read or parsed.
func LoadTrustedKeys(paths []string) ([]ed25519.PublicKey, error) {
	keys := make([]ed25519.PublicKey, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading trusted key %q: %w", path, err)
		}
		pub, err := ParsePublicKeyPEM(data)
		if err != nil {
			return nil, fmt.Errorf("parsing trusted key %q: %w", path, err)
		}
		keys = append(keys, pub)
	}
	return keys, nil
}

// VerifyFile reads a file and its detached signature (.sig), then verifies
// the signature against the trusted keys. Returns nil if verification succeeds.
func VerifyFile(trustedKeys []ed25519.PublicKey, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file %q: %w", filePath, err)
	}

	sigPath := filePath + SignatureFileExtension
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("reading signature %q: %w — run 'denkeeper plugin sign' to create one", sigPath, err)
	}

	if !VerifyWithAnyKey(trustedKeys, data, sig) {
		return fmt.Errorf("signature verification failed for %q: not signed by any trusted key", filePath)
	}
	return nil
}

// SignFile reads a file and writes a detached Ed25519 signature to filePath.sig.
func SignFile(privateKey ed25519.PrivateKey, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file %q: %w", filePath, err)
	}

	sig := Sign(privateKey, data)
	sigPath := filePath + SignatureFileExtension
	if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
		return fmt.Errorf("writing signature %q: %w", sigPath, err)
	}
	return nil
}
