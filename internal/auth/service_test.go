package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// fakeStore 是 ConfigStore 的内存实现，便于在不触碰磁盘的情况下测试认证服务。
type fakeStore struct {
	cfg       config.YAMLConfig
	saveErr   error // 注入 Save 失败，验证错误传播。
	saveCount int
}

func newFakeStore() *fakeStore {
	// 以默认配置为基础：未初始化管理员、会话超时 3600s。
	return &fakeStore{cfg: config.DefaultYAMLConfig()}
}

func (f *fakeStore) Config() config.YAMLConfig {
	return f.cfg
}

func (f *fakeStore) Save(cfg config.YAMLConfig) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	// 模拟 *config.Manager 的语义：保存前做范围校验。
	if err := config.ValidateYAMLConfig(cfg); err != nil {
		return err
	}
	f.cfg = cfg
	f.saveCount++
	return nil
}

// newService 构造一个用于测试的认证服务，使用固定签名密钥。
func newService(t *testing.T, store ConfigStore) *Service {
	t.Helper()
	svc, err := New(store, []byte("test-signing-key-0123456789abcdef"))
	if err != nil {
		t.Fatalf("New 不应返回错误：%v", err)
	}
	return svc
}

// asAPIError 将 error 断言为 *domain.APIError。
func asAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	return apiErr
}

// TestNewRejectsInvalidArgs 验证 New 在缺少必需依赖时返回校验错误。
func TestNewRejectsInvalidArgs(t *testing.T) {
	if _, err := New(nil, []byte("key")); err == nil {
		t.Error("store 为 nil 时应返回错误")
	}
	if _, err := New(newFakeStore(), nil); err == nil {
		t.Error("签名密钥为空时应返回错误")
	}
}

// TestIsInitialized 验证首次初始化状态（Req 1.1）。
func TestIsInitialized(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)
	if svc.IsInitialized() {
		t.Error("默认配置下应处于未初始化状态（提供注册入口）")
	}

	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册不应失败：%v", err)
	}
	if !svc.IsInitialized() {
		t.Error("注册后应处于已初始化状态")
	}
}

// TestRegisterSuccess 验证合法注册写入加盐哈希并完成初始化（Req 1.2）。
func TestRegisterSuccess(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)

	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册不应失败：%v", err)
	}

	got := store.Config().Admin
	if !got.Initialized {
		t.Error("注册后 Initialized 应为 true")
	}
	if got.Username != "admin" {
		t.Errorf("用户名期望 admin，实际 %q", got.Username)
	}
	if got.PasswordHash == "" {
		t.Error("应写入密码哈希")
	}
	if got.PasswordHash == "password123" {
		t.Error("密码哈希不应是明文")
	}
	// 哈希应可校验通过。
	if err := comparePassword(got.PasswordHash, "password123"); err != nil {
		t.Errorf("写入的哈希应能校验通过：%v", err)
	}
}

// TestRegisterRejectsSecondRegistration 验证已存在管理员时拒绝注册、保持单用户（Req 1.3）。
func TestRegisterRejectsSecondRegistration(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)

	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("首次注册不应失败：%v", err)
	}
	firstHash := store.Config().Admin.PasswordHash

	err := svc.Register("intruder", "anotherpass123")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeConflict {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeConflict, apiErr.Code)
	}

	// 现有账号应保持不变。
	after := store.Config().Admin
	if after.Username != "admin" || after.PasswordHash != firstHash {
		t.Error("第二次注册不应更改现有管理员账号")
	}
}

// TestRegisterValidatesLengths 验证用户名/密码长度越界时拒绝且不写入（Req 1.9）。
func TestRegisterValidatesLengths(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		badField string
	}{
		{"用户名过短", "ab", "password123", "username"},
		{"用户名过长", strings.Repeat("a", 33), "password123", "username"},
		{"密码过短", "admin", "short", "password"},
		{"密码过长", "admin", strings.Repeat("p", 129), "password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeStore()
			svc := newService(t, store)

			err := svc.Register(c.username, c.password)
			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields[c.badField]; !ok {
				t.Errorf("期望字段级错误包含 %q，实际 %v", c.badField, apiErr.Fields)
			}
			// 校验失败不应写入任何账号。
			if store.Config().Admin.Initialized {
				t.Error("校验失败时不应完成初始化")
			}
			if store.saveCount != 0 {
				t.Error("校验失败时不应调用 Save")
			}
		})
	}
}

// TestRegisterAcceptsBoundaryLengths 验证边界长度（3/32 用户名、6/128 密码）被接受（Req 1.2）。
func TestRegisterAcceptsBoundaryLengths(t *testing.T) {
	cases := []struct {
		username string
		password string
	}{
		{"abc", "123456"}, // 下界
		{strings.Repeat("u", 32), strings.Repeat("p", 128)}, // 上界
	}
	for _, c := range cases {
		store := newFakeStore()
		svc := newService(t, store)
		if err := svc.Register(c.username, c.password); err != nil {
			t.Errorf("边界长度应被接受：username=%d password=%d，错误：%v",
				len(c.username), len(c.password), err)
		}
	}
}

// TestLoginSuccessIssuesToken 验证凭证匹配时签发有效期=会话超时的令牌（Req 1.4）。
func TestLoginSuccessIssuesToken(t *testing.T) {
	store := newFakeStore()
	store.cfg.Auth.SessionTimeoutS = 1800
	svc := newService(t, store)
	fixed := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	token, expiresAt, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录不应失败：%v", err)
	}
	if token == "" {
		t.Error("应返回非空令牌")
	}
	// 有效期应等于会话超时时长。
	wantExpiry := fixed.Add(1800 * time.Second)
	if !expiresAt.Equal(wantExpiry) {
		t.Errorf("过期时刻期望 %v，实际 %v", wantExpiry, expiresAt)
	}

	// 签发的令牌应可被解析且主体为管理员用户名。
	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("应能解析自身签发的令牌：%v", err)
	}
	if claims.Username != "admin" {
		t.Errorf("令牌主体期望 admin，实际 %q", claims.Username)
	}
}

// TestLoginRejectsBadCredentials 验证用户名或密码不匹配时拒绝登录（Req 1.5）。
func TestLoginRejectsBadCredentials(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	cases := []struct {
		name     string
		username string
		password string
	}{
		{"密码错误", "admin", "wrongpassword"},
		{"用户名错误", "someoneelse", "password123"},
		{"两者均错", "x", "y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			token, _, err := svc.Login(c.username, c.password)
			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeUnauthorized {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
			}
			if token != "" {
				t.Error("拒绝登录时不应返回令牌")
			}
		})
	}
}

// TestLoginBeforeInitialization 验证未初始化时登录按鉴权失败处理（Req 1.5）。
func TestLoginBeforeInitialization(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)

	_, _, err := svc.Login("admin", "password123")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestChangePasswordSuccess 验证当前密码匹配且新密码合法时更新哈希（Req 1.8）。
func TestChangePasswordSuccess(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	oldHash := store.Config().Admin.PasswordHash

	if err := svc.ChangePassword("password123", "newpassword456"); err != nil {
		t.Fatalf("改密不应失败：%v", err)
	}

	newHash := store.Config().Admin.PasswordHash
	if newHash == oldHash {
		t.Error("改密后哈希应发生变化")
	}
	// 新密码应能登录，旧密码应失败。
	if err := comparePassword(newHash, "newpassword456"); err != nil {
		t.Errorf("新密码应能校验通过：%v", err)
	}
	if err := comparePassword(newHash, "password123"); err == nil {
		t.Error("旧密码不应再校验通过")
	}
}

// TestChangePasswordRejectsWrongCurrent 验证当前密码不匹配时拒绝并保留原哈希（Req 1.10）。
func TestChangePasswordRejectsWrongCurrent(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	oldHash := store.Config().Admin.PasswordHash

	err := svc.ChangePassword("wrongcurrent", "newpassword456")
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
	if store.Config().Admin.PasswordHash != oldHash {
		t.Error("当前密码不匹配时应保留原哈希不变")
	}
}

// TestChangePasswordRejectsInvalidNewLength 验证新密码长度越界时拒绝并保留原哈希（Req 1.10）。
func TestChangePasswordRejectsInvalidNewLength(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"新密码过短", "short"},
		{"新密码过长", strings.Repeat("p", 129)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeStore()
			svc := newService(t, store)
			if err := svc.Register("admin", "password123"); err != nil {
				t.Fatalf("注册失败：%v", err)
			}
			oldHash := store.Config().Admin.PasswordHash

			err := svc.ChangePassword("password123", c.password)
			apiErr := asAPIError(t, err)
			if apiErr.Code != domain.CodeValidation {
				t.Errorf("期望错误码 %q，实际 %q", domain.CodeValidation, apiErr.Code)
			}
			if _, ok := apiErr.Fields["newPassword"]; !ok {
				t.Errorf("期望字段级错误包含 newPassword，实际 %v", apiErr.Fields)
			}
			if store.Config().Admin.PasswordHash != oldHash {
				t.Error("新密码非法时应保留原哈希不变")
			}
		})
	}
}

// TestParseTokenRejectsExpired 验证过期令牌被拒绝（Req 1.7 的会话语义基础）。
func TestParseTokenRejectsExpired(t *testing.T) {
	store := newFakeStore()
	store.cfg.Auth.SessionTimeoutS = 300
	svc := newService(t, store)
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	// 在过去签发，令牌应已过期。
	past := time.Now().Add(-2 * time.Hour)
	svc.now = func() time.Time { return past }
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	// 恢复真实时钟后解析应失败。
	svc.now = time.Now
	_, err = svc.ParseToken(token)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeUnauthorized {
		t.Errorf("期望错误码 %q，实际 %q", domain.CodeUnauthorized, apiErr.Code)
	}
}

// TestParseTokenRejectsTampered 验证签名不符的令牌被拒绝。
func TestParseTokenRejectsTampered(t *testing.T) {
	store := newFakeStore()
	svc := newService(t, store)
	if err := svc.Register("admin", "password123"); err != nil {
		t.Fatalf("注册失败：%v", err)
	}
	token, _, err := svc.Login("admin", "password123")
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}

	// 用不同密钥构造的服务无法校验该令牌。
	other, _ := New(store, []byte("a-completely-different-signing-key"))
	if _, err := other.ParseToken(token); err == nil {
		t.Error("使用不同签名密钥应无法校验令牌")
	}
}
