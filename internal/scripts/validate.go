package scripts

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxScriptBytes   = 1 << 20 // 1 MiB
	MaxScriptNameLen = 100
	MaxDescription   = 500
	MaxTags          = 16
	MaxTagLen        = 32
	MaxScripts       = 500
	MaxVersions      = 200
)

var (
	nameOK    = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _.\-]{0,99}$`)
	idOK      = regexp.MustCompile(`^scr_[a-f0-9]{16,32}$`)
	versionOK = regexp.MustCompile(`^v\d+$`)
	sha256OK  = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// DefaultRuntimeForLanguage 返回语言默认解释器基名。
func DefaultRuntimeForLanguage(lang Language) string {
	switch NormalizeLanguage(string(lang)) {
	case LangJavaScript:
		return "node"
	default:
		return "python3"
	}
}

// DefaultEntryFile 返回默认入口文件名。
func DefaultEntryFile(lang Language) string {
	switch NormalizeLanguage(string(lang)) {
	case LangJavaScript:
		return "index.js"
	default:
		return "main.py"
	}
}

// NormalizeLanguage 归一化语言。
func NormalizeLanguage(s string) Language {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "js", "javascript", "node", "mjs", "cjs", "ts", "typescript":
		return LangJavaScript
	case "py", "python", "python3":
		return LangPython
	default:
		return LangPython
	}
}

// NormalizeRuntime 校验并归一化解释器基名。
func NormalizeRuntime(runtime string, lang Language) (string, error) {
	base := strings.ToLower(strings.TrimSpace(runtime))
	if base == "" {
		return DefaultRuntimeForLanguage(lang), nil
	}
	// 仅允许基名，拒绝路径。
	if strings.ContainsAny(base, `/\`) || base != filepath.Base(base) {
		return "", fmt.Errorf("runtime 只能是解释器基名，如 node 或 python3")
	}
	switch NormalizeLanguage(string(lang)) {
	case LangJavaScript:
		if base != "node" {
			return "", fmt.Errorf("javascript 单文件脚本仅支持 node 运行时")
		}
	case LangPython:
		if base != "python" && base != "python3" {
			return "", fmt.Errorf("python 单文件脚本仅支持 python 或 python3 运行时")
		}
	default:
		return "", fmt.Errorf("不支持的脚本语言")
	}
	return base, nil
}

// ValidateScriptName 校验显示名。
func ValidateScriptName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("脚本名称不能为空")
	}
	if utf8.RuneCountInString(name) > MaxScriptNameLen {
		return fmt.Errorf("脚本名称过长")
	}
	if !nameOK.MatchString(name) {
		return fmt.Errorf("脚本名称含非法字符")
	}
	return nil
}

// ValidateContent 校验脚本文本。
func ValidateContent(content string) error {
	if content == "" {
		return fmt.Errorf("脚本内容不能为空")
	}
	if len(content) > MaxScriptBytes {
		return fmt.Errorf("脚本内容超过 %d 字节上限", MaxScriptBytes)
	}
	if strings.ContainsRune(content, 0) {
		return fmt.Errorf("脚本内容包含非法空字符")
	}
	if !utf8.ValidString(content) {
		return fmt.Errorf("脚本内容必须为合法 UTF-8")
	}
	return nil
}

// NormalizeTags 清洗标签。
func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > MaxTagLen {
			t = string([]rune(t)[:MaxTagLen])
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
		if len(out) >= MaxTags {
			break
		}
	}
	return out
}

func validScriptID(id string) bool { return idOK.MatchString(strings.TrimSpace(id)) }
func validVersion(v string) bool   { return versionOK.MatchString(strings.TrimSpace(v)) }

// ValidVersion 报告版本是否为可持久化的不可变版本号（vN）。
func ValidVersion(v string) bool { return validVersion(v) }

// ValidSHA256 报告哈希是否为规范小写 SHA-256。
func ValidSHA256(v string) bool { return sha256OK.MatchString(strings.TrimSpace(v)) }
