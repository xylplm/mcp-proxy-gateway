package crypto

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"
)

// testKey 是用于构造加密服务的固定合法 32 字节密钥（原始字节形式，对应
// AES-256）。其长度恰为 32 字节，按实现的密钥编码方案以最高优先级被当作
// raw 密钥使用。
const testKey = "0123456789abcdef0123456789abcdef"

// Feature: mcp-proxy-gateway, Property 17: 凭证加解密往返
//
// Validates: Requirements 19.1, 19.2
//
// 对任意明文凭证（含空切片），先 Encrypt 再 Decrypt 应得到与原文逐字节相同的
// 值（往返可逆）。此外验证两条与随机 nonce 相关的不变量：
//   - 密文不等于明文（已被加密变换，且前置 nonce 使长度增加）；
//   - 对同一明文两次加密的输出互不相同（每次使用新的随机 nonce）。
func TestProperty17CredentialEncryptDecryptRoundTrip(t *testing.T) {
	svc, err := New(testKey)
	if err != nil {
		t.Fatalf("构造加密服务失败：%v", err)
	}

	rapid.Check(t, func(t *rapid.T) {
		// 生成任意字节切片作为明文凭证，最小长度为 0 以覆盖空切片边界。
		plaintext := rapid.SliceOfN(rapid.Byte(), 0, 256).Draw(t, "plaintext")

		ciphertext, err := svc.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("加密失败：plaintext=%v err=%v", plaintext, err)
		}

		// 往返可逆：Decrypt(Encrypt(p)) == p。
		decrypted, err := svc.Decrypt(ciphertext)
		if err != nil {
			t.Fatalf("解密失败：plaintext=%v err=%v", plaintext, err)
		}
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatalf("往返结果与原文不一致：want=%v got=%v", plaintext, decrypted)
		}

		// 密文不等于明文（已加密且前置 nonce 使内容与长度均不同）。
		if bytes.Equal(ciphertext, plaintext) {
			t.Fatalf("密文不应等于明文：plaintext=%v", plaintext)
		}

		// 随机 nonce：对同一明文两次加密的输出应互不相同。
		ciphertext2, err := svc.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("二次加密失败：plaintext=%v err=%v", plaintext, err)
		}
		if bytes.Equal(ciphertext, ciphertext2) {
			t.Fatalf("两次加密输出不应相同（随机 nonce 失效）：plaintext=%v", plaintext)
		}
	})
}
