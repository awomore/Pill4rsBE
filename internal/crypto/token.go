package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var ErrInvalidCiphertext = errors.New("invalid ciphertext")

// EncryptToken encrypts plaintext with AES-256-GCM using the supplied key and
// returns a base64-encoded string of nonce || ciphertext. The key may be any
// length: a 32-byte key is used directly, otherwise it is hashed with SHA-256
// to produce a valid 256-bit key.
func EncryptToken(plaintext, key string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptToken reverses EncryptToken.
func DecryptToken(encoded, key string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrInvalidCiphertext
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", ErrInvalidCiphertext
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(plaintext), nil
}

func newGCM(key string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func deriveKey(key string) []byte {
	raw := []byte(key)
	if len(raw) == 32 {
		return raw
	}
	sum := sha256.Sum256(raw)
	return sum[:]
}
