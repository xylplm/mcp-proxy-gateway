package risk

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestCipherRoundTripAndUniqueNonce(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	a, nonceA, err := cipher.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	b, nonceB, err := cipher.Encrypt([]byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(nonceA, nonceB) || bytes.Equal(a, b) {
		t.Fatal("每次加密必须使用唯一 nonce")
	}
	plain, err := cipher.Decrypt(a, nonceA)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret-value" {
		t.Fatalf("解密结果 = %q", plain)
	}
	key[0] ^= 0xff
	wrong, _ := NewCipher(base64.StdEncoding.EncodeToString(key))
	if _, err := wrong.Decrypt(a, nonceA); err == nil {
		t.Fatal("错误主密钥不应解密成功")
	}
}

func TestNewCipherRejectsInvalidKey(t *testing.T) {
	if _, err := NewCipher(""); err == nil {
		t.Fatal("空主密钥应被拒绝")
	}
	if _, err := NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("非 32 字节主密钥应被拒绝")
	}
}
