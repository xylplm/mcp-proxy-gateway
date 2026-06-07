package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 管理员凭证长度约束（Req 1.2、1.9、1.8、1.10）。
const (
	// minUsernameLen 为管理员用户名的最小字符数。
	minUsernameLen = 3
	// maxUsernameLen 为管理员用户名的最大字符数。
	maxUsernameLen = 32
	// minPasswordLen 为管理员密码的最小字符数。
	minPasswordLen = 8
	// maxPasswordLen 为管理员密码的最大字符数。
	maxPasswordLen = 128
)

// ConfigStore 是认证服务依赖的配置存储窄接口。
//
// 仅声明本组件实际使用的方法：读取当前 YAML 配置快照与回写持久化。
// *config.Manager 满足该接口；以接口而非具体类型依赖，便于单元测试替换。
type ConfigStore interface {
	// Config 返回当前 YAML 常规配置的快照副本。
	Config() config.YAMLConfig
	// Save 校验并将给定 YAML 配置回写持久化，成功后更新内存快照（Req 1.2、1.8）。
	Save(cfg config.YAMLConfig) error
}

// Claims 为校验通过的访问令牌所携带的会话信息。
type Claims struct {
	// Username 为令牌主体（管理员用户名）。
	Username string
	// ExpiresAt 为令牌的过期时刻。
	ExpiresAt time.Time
}

// Service 是认证服务（Auth_Service）的实现：负责管理员单用户的首次初始化、
// 注册、登录会话签发与改密（Req 1）。
//
// 管理员凭证以 bcrypt 加盐哈希形式存放于 YAML 配置（经 ConfigStore 持久化）；
// 登录成功签发有效期等于配置会话超时时长的 JWT。Service 的方法对并发使用是安全的，
// 其安全性由底层 ConfigStore 的并发保护（*config.Manager 内部读写锁）保证。
type Service struct {
	// store 为配置存储，承载管理员凭证与会话超时配置。
	store ConfigStore
	// signingKey 为 JWT（HMAC-SHA256）签名密钥；来自环境注入的密钥材料。
	signingKey []byte
	// now 返回当前时间，便于在测试中注入可控时钟。
	now func() time.Time
}

// New 构造认证服务。
//
// store 为必需依赖；signingKey 为 JWT 签名密钥，不可为空（否则签发的令牌无从校验）。
// 任一前置条件不满足时返回 VALIDATION 错误。
func New(store ConfigStore, signingKey []byte) (*Service, error) {
	if store == nil {
		return nil, domain.NewError(domain.CodeValidation, "认证服务初始化失败：配置存储为空")
	}
	if len(signingKey) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "认证服务初始化失败：JWT 签名密钥为空")
	}
	return &Service{
		store:      store,
		signingKey: signingKey,
		now:        time.Now,
	}, nil
}

// IsInitialized 报告是否已存在管理员账号（Req 1.1）。
//
// 为 false 时系统处于首次初始化状态，应对外提供管理员注册入口。
func (s *Service) IsInitialized() bool {
	return s.store.Config().Admin.Initialized
}

// Register 注册唯一的管理员账号并完成首次初始化（Req 1.2、1.3、1.9）。
//
// 流程：若已存在管理员则拒绝以保持单用户（Req 1.3）→ 校验用户名长度（3-32）与
// 密码长度（8-128，Req 1.9）→ 以 bcrypt 加盐哈希写入 YAML 配置并置初始化标志（Req 1.2）。
//
// 错误语义：
//   - 已存在管理员账号：返回 CONFLICT，保留现有账号不变（Req 1.3）。
//   - 用户名或密码长度越界：返回携带字段级说明的 VALIDATION，不写入任何账号（Req 1.9）。
func (s *Service) Register(username, password string) error {
	cfg := s.store.Config()
	// 已初始化则拒绝注册，保持单用户管理并保留现有账号不变（Req 1.3）。
	if cfg.Admin.Initialized {
		return domain.NewError(domain.CodeConflict, "管理员账号已存在，系统保持单用户管理")
	}

	if err := validateCredentials(username, password); err != nil {
		return err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	cfg.Admin.Username = username
	cfg.Admin.PasswordHash = hash
	cfg.Admin.Initialized = true
	if err := s.store.Save(cfg); err != nil {
		return err
	}
	return nil
}

// Login 校验管理员凭证，匹配则签发会话令牌（Req 1.4、1.5）。
//
// 凭证匹配时返回一个有效期等于配置会话超时时长（默认 3600 秒，范围 300-86400）的
// JWT 及其过期时刻（Req 1.4）。用户名或密码任一不匹配时返回 UNAUTHORIZED，
// 不创建任何会话（Req 1.5）。
//
// 为降低旁路时序差异，无论用户名是否匹配都执行一次哈希比对，避免据响应时间区分
// 「用户名不存在」与「密码错误」。
func (s *Service) Login(username, password string) (token string, expiresAt time.Time, err error) {
	cfg := s.store.Config()
	if !cfg.Admin.Initialized {
		// 尚未初始化管理员，无凭证可匹配，按鉴权失败处理（Req 1.5）。
		return "", time.Time{}, domain.NewError(domain.CodeUnauthorized, "用户名或密码错误")
	}

	usernameMatch := username == cfg.Admin.Username
	pwErr := comparePassword(cfg.Admin.PasswordHash, password)
	if !usernameMatch || pwErr != nil {
		return "", time.Time{}, domain.NewError(domain.CodeUnauthorized, "用户名或密码错误")
	}

	timeout := time.Duration(cfg.Auth.SessionTimeoutS) * time.Second
	return s.issueToken(cfg.Admin.Username, timeout)
}

// ChangePassword 校验当前密码与新密码长度后更新密码哈希（Req 1.8、1.10）。
//
// 流程：校验当前密码是否与已存储哈希匹配（Req 1.10）→ 校验新密码长度（8-128，Req 1.10）
// → 以新密码的 bcrypt 哈希更新并写入 YAML 配置（Req 1.8）。
//
// 错误语义：
//   - 当前密码不匹配：返回 UNAUTHORIZED，保留原密码哈希不变（Req 1.10）。
//   - 新密码长度越界：返回携带字段级说明的 VALIDATION，保留原密码哈希不变（Req 1.10）。
func (s *Service) ChangePassword(currentPassword, newPassword string) error {
	cfg := s.store.Config()
	if !cfg.Admin.Initialized {
		return domain.NewError(domain.CodeUnauthorized, "尚未初始化管理员账号，无法修改密码")
	}

	// 先校验当前密码，不匹配则拒绝并保留原哈希（Req 1.10）。
	if err := comparePassword(cfg.Admin.PasswordHash, currentPassword); err != nil {
		return domain.NewError(domain.CodeUnauthorized, "当前密码不正确，密码修改失败")
	}

	// 再校验新密码长度（Req 1.10）。
	if n := utf8.RuneCountInString(newPassword); n < minPasswordLen || n > maxPasswordLen {
		return domain.NewValidationError("密码修改失败", map[string]string{
			"newPassword": "新密码长度需在 8 至 128 个字符之间",
		})
	}

	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	cfg.Admin.PasswordHash = hash
	if err := s.store.Save(cfg); err != nil {
		return err
	}
	return nil
}

// ParseToken 校验并解析访问令牌，返回其会话信息（Req 1.4 的会话语义）。
//
// 校验包含签名方法（仅接受 HMAC）、签名有效性与过期时间；令牌缺失签名不符、
// 被篡改或已过期时返回 UNAUTHORIZED。该方法供会话校验复用，过期判定由 jwt 库
// 依据 ExpiresAt 自动完成。
func (s *Service) ParseToken(tokenString string) (Claims, error) {
	parsed, err := jwt.ParseWithClaims(
		tokenString,
		&jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("非预期的签名方法：%v", t.Header["alg"])
			}
			return s.signingKey, nil
		},
		// 过期判定使用与签发一致的时钟，保证会话超时语义可控可测（Req 1.7）。
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !parsed.Valid {
		msg := "令牌无效或已过期"
		if err != nil {
			msg += "：" + err.Error()
		}
		return Claims{}, domain.NewError(domain.CodeUnauthorized, msg)
	}

	rc, ok := parsed.Claims.(*jwt.RegisteredClaims)
	if !ok || rc.ExpiresAt == nil {
		return Claims{}, domain.NewError(domain.CodeUnauthorized, "令牌缺少必要的声明")
	}
	return Claims{Username: rc.Subject, ExpiresAt: rc.ExpiresAt.Time}, nil
}

// issueToken 签发一个以 username 为主体、有效期为 timeout 的 HS256 JWT（Req 1.4）。
func (s *Service) issueToken(username string, timeout time.Duration) (string, time.Time, error) {
	now := s.now()
	expiresAt := now.Add(timeout)
	claims := jwt.RegisteredClaims{
		Subject:   username,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.signingKey)
	if err != nil {
		return "", time.Time{}, domain.NewError(domain.CodeValidation, "签发访问令牌失败："+err.Error())
	}
	return signed, expiresAt, nil
}

// validateCredentials 校验注册时的用户名（3-32）与密码（8-128）长度（按 Unicode 字符计数）。
//
// 不通过时返回携带字段级说明的 VALIDATION 错误，调用方据此可定位每个无效字段（Req 1.9）。
func validateCredentials(username, password string) error {
	fields := make(map[string]string)
	if n := utf8.RuneCountInString(username); n < minUsernameLen || n > maxUsernameLen {
		fields["username"] = "用户名长度需在 3 至 32 个字符之间"
	}
	if n := utf8.RuneCountInString(password); n < minPasswordLen || n > maxPasswordLen {
		fields["password"] = "密码长度需在 8 至 128 个字符之间"
	}
	if len(fields) > 0 {
		return domain.NewValidationError("管理员注册校验失败", fields)
	}
	return nil
}

// hashPassword 计算密码的加盐哈希（Req 1.2）。
//
// 由于 bcrypt 仅处理输入的前 72 个字节，而需求允许的密码长度上限为 128 个字符
// （ASCII 即 128 字节，多字节字符更长，均可能超过 72 字节并被 bcrypt 拒绝或截断），
// 因此先以 SHA-256 将任意长度密码归一为定长摘要、再做 base64 编码（44 字节，<72），
// 最后交由 bcrypt 加盐哈希。如此既保留 bcrypt 的加盐慢哈希优势，又能完整覆盖
// 8-128 字符的全部合法密码而不丢失任何熵。
func hashPassword(password string) (string, error) {
	prepared := prehash(password)
	hash, err := bcrypt.GenerateFromPassword(prepared, bcrypt.DefaultCost)
	if err != nil {
		return "", domain.NewError(domain.CodeValidation, "密码哈希失败："+err.Error())
	}
	return string(hash), nil
}

// comparePassword 校验明文密码是否与已存储的 bcrypt 哈希匹配（Req 1.4、1.5、1.10）。
//
// 必须与 hashPassword 采用相同的预处理（SHA-256 + base64），否则比对必然失败。
// 匹配返回 nil，不匹配返回非 nil 错误。
func comparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), prehash(password))
}

// prehash 将任意长度密码归一为适配 bcrypt 72 字节上限的定长输入。
func prehash(password string) []byte {
	digest := sha256.Sum256([]byte(password))
	encoded := base64.StdEncoding.EncodeToString(digest[:])
	return []byte(encoded)
}
