package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// 离线密码重置（Req 1.x 扩展）：
//
// 当管理员忘记密码时，无需暴露任何带鉴权的 HTTP 端点（避免产生远程攻击面）。
// 改为约定一个本地标记文件：在 data 目录下创建空文件 `.reset-admin`，
// 重启网关后启动期会自动：
//
//  1. 检测该文件是否存在；存在即触发管理员密码重置。
//  2. 生成一个 16 字节、URL-safe 的随机新密码。
//  3. 用 bcrypt 加盐哈希写回 YAML（保留原用户名）；若尚未初始化则按用户名 "admin" 初始化。
//  4. 在控制台（slog）以醒目格式打印一次新密码（仅此一次）。
//  5. 删除该标记文件，避免重复重置。
//
// 安全约束：标记文件位于挂载的 data 目录，能创建该文件的攻击者已获得文件系统写权限，
// 因此该机制不引入额外的远程攻击面；密码仅打印到 stdout/stderr，不持久化日志。

// resetMarkerName 为离线密码重置标记文件名。
const resetMarkerName = ".reset-admin"

// resetUsernameFallback 为重置时的兜底用户名（仅在尚未初始化或原用户名为空时使用）。
const resetUsernameFallback = "admin"

// ResetMarkerPath 返回 data 目录下离线密码重置标记文件的完整路径。
//
// 暴露此函数便于装配层与前端文档协作（前端在「忘记密码」弹窗中提示用户该路径文件名）。
func ResetMarkerPath(dataDir string) string {
	return filepath.Join(dataDir, resetMarkerName)
}

// ResetMarkerFileName 返回标记文件名（不含目录），用于前端提示文案。
func ResetMarkerFileName() string {
	return resetMarkerName
}

// MaybeResetAdminPassword 检查并执行离线密码重置（启动期调用）。
//
// 工作流程：
//   - 标记文件不存在：直接返回（noop）。
//   - 存在：生成随机新密码，写回 YAML（bcrypt 哈希），用 slog 打印新密码，
//     再删除标记文件。任一步失败都会记录错误并保留标记文件以便排查。
//
// 该函数对 store 与 logger 必须非空；否则视为装配错误，直接返回错误。
// 仅在装配阶段调用一次（见 internal/app/build.go），运行期不再触发。
func MaybeResetAdminPassword(store ConfigStore, dataDir string, logger *slog.Logger) error {
	if store == nil || logger == nil || dataDir == "" {
		return fmt.Errorf("离线密码重置参数无效：store/logger/dataDir 不可为空")
	}

	markerPath := ResetMarkerPath(dataDir)
	if _, err := os.Stat(markerPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("检查离线重置标记文件失败：%w", err)
	}

	// 生成随机新密码（16 字节 → 22 字符 base64url，符合 6-128 长度约束）。
	newPassword, err := generateRandomPassword()
	if err != nil {
		logger.Error("离线密码重置失败：生成随机密码出错", "error", err)
		return err
	}

	hash, err := hashPassword(newPassword)
	if err != nil {
		logger.Error("离线密码重置失败：密码哈希出错", "error", err)
		return err
	}

	cfg := store.Config()
	username := cfg.Admin.Username
	if username == "" {
		username = resetUsernameFallback
	}
	cfg.Admin.Username = username
	cfg.Admin.PasswordHash = hash
	cfg.Admin.Initialized = true
	if err := store.Save(cfg); err != nil {
		logger.Error("离线密码重置失败：写回 YAML 出错", "error", err)
		return err
	}

	// 在控制台醒目展示一次性新密码：用 slog 单条输出，避免被多行日志淹没。
	logger.Warn(
		"==== 管理员密码已被离线重置 ==== 请立即记录并妥善保存（此密码仅显示一次）",
		"username", username,
		"newPassword", newPassword,
	)

	// 删除标记文件，避免下次重启重复重置。
	if err := os.Remove(markerPath); err != nil {
		logger.Error("离线密码重置：删除标记文件失败，请手动删除以避免下次重启再次重置",
			"path", markerPath, "error", err)
		return nil
	}
	return nil
}

// generateRandomPassword 生成一个 16 字节随机熵的 URL-safe base64 密码（22 字符）。
//
// 使用 crypto/rand 提供的密码学安全随机源，长度足以满足 6-128 字符的密码长度约束。
func generateRandomPassword() (string, error) {
	const entropyBytes = 16
	buf := make([]byte, entropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
