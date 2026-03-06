package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

var gcm cipher.AEAD

// Init loads ENCRYPTION_KEY from env and initializes AES-256-GCM.
// If the key is not set, encryption features are unavailable.
func Init() error {
	keyHex := os.Getenv("ENCRYPTION_KEY")
	if keyHex == "" {
		return nil // encryption disabled — env vars fallback only
	}

	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return errors.New("ENCRYPTION_KEY must be a valid hex string")
	}
	if len(key) != 32 {
		return errors.New("ENCRYPTION_KEY must be 64 hex chars (32 bytes / AES-256)")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	gcm, err = cipher.NewGCM(block)
	if err != nil {
		return err
	}

	return nil
}

// Available returns true if encryption was initialized successfully.
func Available() bool {
	return gcm != nil
}

// Encrypt encrypts plaintext and returns a hex-encoded string (nonce + ciphertext).
func Encrypt(plaintext string) (string, error) {
	if gcm == nil {
		return "", errors.New("encryption not initialized")
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a hex-encoded string (nonce + ciphertext) and returns plaintext.
func Decrypt(encoded string) (string, error) {
	if gcm == nil {
		return "", errors.New("encryption not initialized")
	}

	data, err := hex.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
