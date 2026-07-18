package scripts

import (
	"fmt"
	"strings"
)

const maxDiffLines = 400

// DiffText 生成简单行级 unified-style hunks（可读摘要，非完整 git diff）。
func DiffText(leftLabel, left, rightLabel, right string) DiffResult {
	lLines := strings.Split(left, "\n")
	rLines := strings.Split(right, "\n")
	hunks := make([]string, 0, 32)
	truncated := false

	// LCS 过重；用双指针扫相同前缀/后缀后对中段做逐行对齐（够用）。
	i, j := 0, 0
	for i < len(lLines) && j < len(rLines) {
		if lLines[i] == rLines[j] {
			// 相同行不输出，保持简洁
			i++
			j++
			continue
		}
		// 向前看是否删除/插入
		if i+1 < len(lLines) && lLines[i+1] == rLines[j] {
			hunks = append(hunks, fmt.Sprintf("-%d|%s", i+1, truncateLine(lLines[i])))
			i++
		} else if j+1 < len(rLines) && lLines[i] == rLines[j+1] {
			hunks = append(hunks, fmt.Sprintf("+%d|%s", j+1, truncateLine(rLines[j])))
			j++
		} else {
			hunks = append(hunks, fmt.Sprintf("-%d|%s", i+1, truncateLine(lLines[i])))
			hunks = append(hunks, fmt.Sprintf("+%d|%s", j+1, truncateLine(rLines[j])))
			i++
			j++
		}
		if len(hunks) >= maxDiffLines {
			truncated = true
			break
		}
	}
	if !truncated {
		for ; i < len(lLines); i++ {
			hunks = append(hunks, fmt.Sprintf("-%d|%s", i+1, truncateLine(lLines[i])))
			if len(hunks) >= maxDiffLines {
				truncated = true
				break
			}
		}
	}
	if !truncated {
		for ; j < len(rLines); j++ {
			hunks = append(hunks, fmt.Sprintf("+%d|%s", j+1, truncateLine(rLines[j])))
			if len(hunks) >= maxDiffLines {
				truncated = true
				break
			}
		}
	}
	if hunks == nil {
		hunks = []string{}
	}
	return DiffResult{
		LeftLabel:  leftLabel,
		RightLabel: rightLabel,
		Hunks:      hunks,
		Truncated:  truncated,
	}
}

func truncateLine(s string) string {
	const max = 240
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
