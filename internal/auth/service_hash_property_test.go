package auth

import (
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// genValidPassword 生成符合管理员密码长度约束（8-128 个 Unicode 字符）的任意密码。
//
// 长度以 rune 计，与实现中 utf8.RuneCountInString 的计数口径一致，从而覆盖含多字节
// 字符的合法密码；不限制字节长度（maxLen 取 -1），以验证 SHA-256+base64 预处理能
// 突破 bcrypt 72 字节上限、完整覆盖 8-128 字符的全部合法密码。
func genValidPassword() *rapid.Generator[string] {
	return rapid.StringN(minPasswordLen, maxPasswordLen, -1)
}

// Feature: mcp-proxy-gateway, Property 28: 管理员凭证哈希往返校验
//
// Validates: Requirements 1.2, 1.5, 1.9
//
// 针对密码哈希逻辑（hashPassword / comparePassword，含 SHA-256+base64 预处理 + bcrypt）
// 验证三条不变量：
//   - 往返成功：对任意合法密码（8-128 字符）哈希后，用相同密码校验通过（Req 1.2、1.5）；
//   - 错误密码失败：用任意不同的密码校验必然失败，不被误判为匹配（Req 1.5）；
//   - 哈希非明文：bcrypt 输出不等于明文密码，确保不以明文形态存储凭证（Req 1.2）。
//
// 注意 bcrypt 为慢哈希：每次迭代仅做 1 次哈希 + 2 次比对，以在 rapid 默认 100 次迭代
// 下控制总开销，避免超时。
func TestProperty28AdminCredentialHashRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		password := genValidPassword().Draw(t, "password")

		hash, err := hashPassword(password)
		if err != nil {
			t.Fatalf("合法密码哈希不应失败：len=%d err=%v",
				utf8.RuneCountInString(password), err)
		}

		// 哈希非明文：存储形态不应等于明文密码。
		if hash == password {
			t.Fatalf("哈希结果不应等于明文：password=%q", password)
		}

		// 往返成功：相同密码必须校验通过。
		if err := comparePassword(hash, password); err != nil {
			t.Fatalf("正确密码校验应通过：password=%q err=%v", password, err)
		}

		// 错误密码失败：构造一个与正确密码不同的密码，校验必须失败。
		wrong := rapid.StringN(0, maxPasswordLen+2, -1).Draw(t, "wrongPassword")
		if wrong == password {
			// 极少数情况下随机得到与正确密码相同的串，追加一个字符确保其不同。
			wrong = password + "#"
		}
		if err := comparePassword(hash, wrong); err == nil {
			t.Fatalf("错误密码不应校验通过：password=%q wrong=%q", password, wrong)
		}
	})
}
