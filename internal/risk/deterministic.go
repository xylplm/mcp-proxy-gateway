package risk

import (
	"regexp"
	"sort"
	"strings"
)

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func DeterministicAssessment(name, description string) DeterministicResult {
	normalizedName := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(name), "_"), "_")
	text := normalizedName + " " + strings.ToLower(description)
	tags := map[string]struct{}{}
	floor := LevelLow

	set := func(level Level, values ...string) {
		floor = MaxLevel(floor, level)
		for _, value := range values {
			tags[value] = struct{}{}
		}
	}

	readOnlyName := hasPrefixToken(normalizedName, "get", "list", "search", "read", "query", "describe", "status", "check")
	if readOnlyName {
		tags["read"] = struct{}{}
	}

	if containsAny(text, "clear_recycle", "empty_recycle", "factory_reset", "format_disk", "format_storage", "destroy_storage", "arbitrary_api", "raw_request") {
		set(LevelBlocked, "irreversible")
	}
	if containsAny(text, "execute_shell", "exec_shell", "shell_command", "arbitrary command", "任意命令", "任意 shell") {
		set(LevelBlocked, "execute")
	}

	destructiveName := hasActionToken(normalizedName, "delete", "destroy", "purge", "erase", "remove")
	if destructiveName && !(readOnlyName && containsAny(text, "deleted_items", "deleted item", "已删除", "回收站列表")) {
		set(LevelHigh, "delete")
		if containsAny(text, "永久", "不可恢复", "irreversible", "permanent") {
			tags["irreversible"] = struct{}{}
		}
	}
	if hasActionToken(normalizedName, "shutdown", "reboot", "migrate", "rollback") || containsAny(text, "stop_node") {
		set(LevelHigh, "shutdown")
	}
	if containsAny(text, "token", "credential", "secret", "password", "role", "permission", "acl", "firewall") &&
		(hasActionToken(normalizedName, "create", "update", "set", "change", "delete", "remove", "grant", "revoke", "rotate") || containsAny(text, "修改", "变更", "授权", "撤销")) {
		set(LevelHigh, "permission", "credential")
	}
	if hasActionToken(normalizedName, "exec", "execute", "command") {
		set(LevelHigh, "execute")
	}

	if hasActionToken(normalizedName, "create", "update", "patch", "write", "upload", "move", "sync", "refresh", "import") {
		set(LevelMedium, "write")
	}
	if containsAny(text, "send", "publish", "发送", "发布") && !readOnlyName {
		set(LevelHigh, "send", "publish")
	}

	out := make([]string, 0, len(tags))
	for tag := range tags {
		out = append(out, tag)
	}
	sort.Strings(out)
	return DeterministicResult{Floor: floor, Tags: out}
}

func hasPrefixToken(name string, tokens ...string) bool {
	for _, token := range tokens {
		if name == token || strings.HasPrefix(name, token+"_") {
			return true
		}
	}
	return false
}

func hasActionToken(name string, tokens ...string) bool {
	parts := strings.Split(name, "_")
	for _, part := range parts {
		for _, token := range tokens {
			if part == token {
				return true
			}
		}
	}
	return false
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
