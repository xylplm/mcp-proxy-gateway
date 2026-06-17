package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
)

// JWT 签名密钥首次生成：
//
// 管理员登录使用 HS256 JWT，需要一个签名密钥。为避免把密钥写死进二进制（开源场景下
// 等于全网共享管理员密钥，任何人可伪造任意部署者的登录），且做到零配置开箱即用，
// 约定：首启时若 config.yaml 中 jwt_secret 为空，则用 crypto/rand 生成 32 字节随机熵、
// base64url 编码后写回 YAML。生成后每个部署实例持有唯一密钥，重启后保持稳定。

// jwtSecretEntropy 为随机生成 JWT 签名密钥所用的熵字节数（32 字节 → 256 位）。
const jwtSecretEntropy = 32

// EnsureJWTSecret 确保 config.yaml 中存在非空的 JWT 签名密钥（启动期调用）。
//
// 工作流程：
//   - 当前 jwt_secret 非空：直接返回（noop）。
//   - 为空：用 crypto/rand 生成 32 字节随机熵并 base64url 编码，写回 YAML，
//     随后用 slog 记录一次「已自动生成」。
//
// 该函数对 store 与 logger 必须非空；否则视为装配错误，直接返回错误。
// 仅在装配阶段调用一次（见 internal/app/build.go），需在 auth.New 之前执行，
// 以保证 auth.New 拿到非空密钥。
func EnsureJWTSecret(store ConfigStore, logger *slog.Logger) error {
	if store == nil || logger == nil {
		return fmt.Errorf("JWT 密钥生成参数无效：store/logger 不可为空")
	}

	cfg := store.Config()
	if cfg.JWTSecret != "" {
		return nil
	}

	secret, err := generateRandomSecret(jwtSecretEntropy)
	if err != nil {
		logger.Error("生成 JWT 签名密钥失败", "error", err)
		return err
	}
	cfg.JWTSecret = secret
	if err := store.Save(cfg); err != nil {
		logger.Error("写回 JWT 签名密钥到 YAML 失败", "error", err)
		return err
	}
	logger.Info("已自动生成 JWT 签名密钥并写入 config.yaml（jwt_secret），后续登录将使用该密钥")
	return nil
}

// generateRandomSecret 生成 n 字节随机熵的 URL-safe base64 字符串。
//
// 使用 crypto/rand 提供的密码学安全随机源。
func generateRandomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
