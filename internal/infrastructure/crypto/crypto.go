// Package crypto encrypts stored secrets (Google refresh tokens, Dify keys) with AES-256-GCM.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

type AESGCM struct{ aead cipher.AEAD }

// New accepts a 32-byte key as 64 hex characters or base64.
func New(key string) (*AESGCM, error) {
	var raw []byte
	var err error
	if len(key) == 64 {
		raw, err = hex.DecodeString(key)
	} else {
		raw, err = base64.StdEncoding.DecodeString(key)
	}
	if err != nil || len(raw) != 32 {
		return nil, errors.New("ENCRYPTION_KEY must be 32 bytes (64 hex chars or base64)")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESGCM{aead}, nil
}

// Encrypt returns "iv.tag.ciphertext" in base64, the same format the Node version used.
func (c *AESGCM) Encrypt(plain string) (string, error) {
	iv := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nil, iv, []byte(plain), nil)
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	enc := base64.StdEncoding.EncodeToString
	return enc(iv) + "." + enc(tag) + "." + enc(ct), nil
}

func (c *AESGCM) Decrypt(payload string) (string, error) {
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", errors.New("bad ciphertext")
	}
	var b [3][]byte
	for i, p := range parts {
		v, err := base64.StdEncoding.DecodeString(p)
		if err != nil {
			return "", err
		}
		b[i] = v
	}
	plain, err := c.aead.Open(nil, b[0], append(b[2], b[1]...), nil)
	return string(plain), err
}

func SHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
