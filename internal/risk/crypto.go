package risk

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type Cipher struct{ aead cipher.AEAD }

func NewCipher(encodedKey string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("MPG_SECRET_ENCRYPTION_KEY 必须是 32 字节密钥的 base64 编码")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-256 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM 失败: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) ([]byte, []byte, error) {
	if c == nil || c.aead == nil {
		return nil, nil, fmt.Errorf("Provider 密钥加密器未配置")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("生成加密 nonce 失败: %w", err)
	}
	return c.aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func (c *Cipher) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, fmt.Errorf("Provider 密钥解密器未配置")
	}
	if len(nonce) != c.aead.NonceSize() {
		return nil, fmt.Errorf("Provider 密钥 nonce 长度无效")
	}
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("Provider API Key 解密失败: %w", err)
	}
	return plain, nil
}
