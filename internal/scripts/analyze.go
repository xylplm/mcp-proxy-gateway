package scripts

import (
	"regexp"
	"strings"
)

type ruleDef struct {
	id       string
	severity RiskLevel
	score    int
	message  string
	re       *regexp.Regexp
}

var riskRules = []ruleDef{
	{id: "shell_exec", severity: RiskCritical, score: 40, message: "疑似调用系统 shell 或任意命令执行", re: regexp.MustCompile(`(?i)\b(os\.system|subprocess\.|child_process|execSync|spawnSync|Popen\s*\()`)},
	{id: "eval", severity: RiskHigh, score: 25, message: "使用动态代码执行（eval/Function/exec）", re: regexp.MustCompile(`(?i)\b(eval\s*\(|new\s+Function\s*\(|\bexec\s*\()`)},
	{id: "network", severity: RiskMedium, score: 12, message: "包含网络访问能力", re: regexp.MustCompile(`(?i)\b(fetch\s*\(|axios\.|http\.|https\.|requests\.|urllib|websocket|socket\.)`)},
	{id: "filesystem", severity: RiskMedium, score: 10, message: "包含文件系统读写", re: regexp.MustCompile(`(?i)\b(fs\.|open\s*\(|readFile|writeFile|pathlib|shutil\.|os\.remove|unlink\s*\()`)},
	{id: "privilege", severity: RiskHigh, score: 20, message: "涉及提权或危险宿主工具", re: regexp.MustCompile(`(?i)\b(sudo\b|powershell|pwsh|chmod\s+|chown\s+)`)},
	{id: "download_exec", severity: RiskCritical, score: 35, message: "疑似下载后执行远程内容", re: regexp.MustCompile(`(?i)(curl\s+.+\|\s*sh|wget\s+.+\|\s*sh|iwr\s+.+\|\s*iex)`)},
	{id: "path_escape", severity: RiskHigh, score: 18, message: "包含路径穿越片段", re: regexp.MustCompile(`\.\./|\.\.\\`)},
}

// AnalyzeContent 对脚本内容做静态风险扫描（不执行）。
func AnalyzeContent(content string) RiskReport {
	lines := strings.Split(content, "\n")
	findings := make([]Finding, 0, 8)
	score := 0
	seen := map[string]struct{}{}

	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "//") {
			continue
		}
		for _, rule := range riskRules {
			if !rule.re.MatchString(line) {
				continue
			}
			key := rule.id + ":" + itoa(i+1)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			findings = append(findings, Finding{
				Rule:     rule.id,
				Severity: rule.severity,
				Line:     i + 1,
				Message:  rule.message,
			})
			score += rule.score
		}
	}

	// 超长单行可能是混淆
	for i, line := range lines {
		if len(line) > 400 {
			findings = append(findings, Finding{
				Rule:     "long_line",
				Severity: RiskMedium,
				Line:     i + 1,
				Message:  "存在超长单行，可能为压缩/混淆代码",
			})
			score += 8
			break
		}
	}

	if score > 100 {
		score = 100
	}
	level := RiskLow
	switch {
	case score >= 70:
		level = RiskCritical
	case score >= 40:
		level = RiskHigh
	case score >= 15:
		level = RiskMedium
	default:
		level = RiskLow
	}
	// 任一条 critical 至少 high
	for _, f := range findings {
		if f.Severity == RiskCritical && level != RiskCritical {
			level = RiskCritical
			break
		}
	}
	if findings == nil {
		findings = []Finding{}
	}
	return RiskReport{Level: level, Score: score, Findings: findings}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
