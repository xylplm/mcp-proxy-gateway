package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// Service 是加密服务（Encryption_Service）的实现：基于 AES-GCM 对上游鉴权
// 凭证进行加解密。Service 在构造时完成密钥的解码与有效性校验，构造成功后
// 即持有一个可复用的 AEAD（带认证的加密）实例，并发安全。
//
// 设计边界：Service 只负责纯粹的加解密与启动期密钥校验，不感知任何业务语义
// （Req 19.1、19.2、19.4）。
type Service struct {
	// aead 为预先初始化的 AES-GCM AEAD 实例，可被并发复用。
	aead cipher.AEAD
}

// 密钥编码方案（已明确并文档化）：
//
// MPG_ENCRYPTION_KEY 支持三种书写形式，按以下固定优先级依次尝试解析，
// 第一个解码后字节长度为合法 AES 密钥长度（16/24/32 字节，分别对应
// AES-128/192/256）的形式即被采用：
//
//  1. 原始字节（raw）：当密钥串本身的字节长度恰为 16、24 或 32 时，直接将其
//     原始字节用作密钥。因此一个 32 字节的密钥串将被解释为 AES-256，这也是
//     推荐的默认用法。
//  2. 十六进制（hex）：当密钥串为合法十六进制且解码后为 16/24/32 字节时采用。
//     例如 `openssl rand -hex 32` 产生的 64 个十六进制字符解码为 32 字节。
//  3. 标准 base64：当密钥串为合法 base64（含填充）且解码后为 16/24/32 字节时
//     采用。例如 `openssl rand -base64 32` 产生的 44 字符解码为 32 字节。
//
// 将「原始字节」置于最高优先级，可保证一个 32 字节的密钥串稳定地被当作
// AES-256 主密钥，符合设计中以 AES-256 为主的约定。常见的 64 位十六进制串与
// 44 位 base64 串因其原始字节长度并非合法 AES 长度，不会与 raw 形式冲突。
//
// 任何形式均无法得到合法长度时，视为密钥无效。

// validAESKeyLength 判断给定字节长度是否为合法的 AES 密钥长度。
func validAESKeyLength(n int) bool {
	return n == 16 || n == 24 || n == 32
}

// decodeKey 按文档化的优先级（raw → hex → base64）将密钥串解码为原始密钥字节。
//
// 解析成功返回解码后的密钥字节；当任何形式都无法得到 16/24/32 字节的合法密钥时
// 返回 false。
func decodeKey(key string) ([]byte, bool) {
	// 1. 原始字节：密钥串本身长度即为合法 AES 长度。
	if validAESKeyLength(len(key)) {
		return []byte(key), true
	}

	// 2. 十六进制解码。
	if decoded, err := hex.DecodeString(key); err == nil && validAESKeyLength(len(decoded)) {
		return decoded, true
	}

	// 3. 标准 base64 解码（含填充）。
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && validAESKeyLength(len(decoded)) {
		return decoded, true
	}

	return nil, false
}

// New 使用环境变量 MPG_ENCRYPTION_KEY 提供的密钥串构造加密服务，并在此完成
// 启动期密钥校验（Req 19.4）。
//
// 校验规则：密钥不可为空；其按上文「密钥编码方案」解码后必须为 16、24 或 32
// 字节（推荐 32 字节即 AES-256）。任一条件不满足时返回 VALIDATION 类错误，
// 调用方（启动流程）应据此用 slog 记录错误并终止启动。
func New(key string) (*Service, error) {
	if key == "" {
		return nil, domain.NewError(
			domain.CodeValidation,
			"加密密钥缺失：环境变量 MPG_ENCRYPTION_KEY 未设置或为空",
		)
	}

	rawKey, ok := decodeKey(key)
	if !ok {
		return nil, domain.NewError(
			domain.CodeValidation,
			"加密密钥无效：MPG_ENCRYPTION_KEY 解码后长度需为 16/24/32 字节"+
				"（原始字节、十六进制或 base64），推荐 32 字节用于 AES-256",
		)
	}

	block, err := aes.NewCipher(rawKey)
	if err != nil {
		// 正常情况下 rawKey 长度已校验为合法 AES 长度，此处属于防御性分支。
		return nil, domain.NewError(
			domain.CodeValidation,
			"加密密钥无效：初始化 AES 分组密码失败："+err.Error(),
		)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, domain.NewError(
			domain.CodeValidation,
			"加密密钥无效：初始化 AES-GCM 失败："+err.Error(),
		)
	}

	return &Service{aead: aead}, nil
}

// Encrypt 使用 AES-GCM 对明文加密（Req 19.1）。
//
// 每次加密都会生成一个随机 nonce，并将该 nonce 前置（prepend）到密文之前，
// 返回的字节布局为 `nonce || ciphertext+tag`。因此即使对相同明文多次加密，
// 输出也各不相同；解密时无需额外传递 nonce。
func (s *Service) Encrypt(plaintext []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	// 预分配 nonce 空间并在其后追加密文，使 nonce 自然前置于结果。
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, domain.NewError(
			domain.CodeValidation,
			"加密失败：生成随机 nonce 出错："+err.Error(),
		)
	}

	// Seal 将密文与认证标签追加到第一个参数（此处为 nonce）之后，
	// 最终得到 nonce || ciphertext+tag。
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 使用 AES-GCM 对 Encrypt 产生的密文解密（Req 19.2）。
//
// 入参 ciphertext 的布局须为 `nonce || ciphertext+tag`：方法先从头部取出
// nonce，再对其余部分做带认证的解密。密文长度不足或认证校验失败时返回错误。
func (s *Service) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, domain.NewError(
			domain.CodeValidation,
			"解密失败：密文长度不足，缺少前置 nonce",
		)
	}

	nonce, encrypted := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, domain.NewError(
			domain.CodeValidation,
			"解密失败：密文被篡改或密钥不匹配："+err.Error(),
		)
	}
	return plaintext, nil
}
