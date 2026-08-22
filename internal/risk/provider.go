package risk

import (
	"fmt"
	"net/url"
	"strings"
)

func ValidateProvider(p Provider) error {
	if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 100 {
		return fmt.Errorf("Provider 名称长度需为 1 至 100")
	}
	if strings.TrimSpace(p.Model) == "" || len([]rune(p.Model)) > 200 {
		return fmt.Errorf("模型名称长度需为 1 至 200")
	}
	if p.APIStyle != APIStyleChatCompletions && p.APIStyle != APIStyleResponses {
		return fmt.Errorf("不支持的 API 风格 %q", p.APIStyle)
	}
	u, err := url.ParseRequestURI(p.BaseURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("Provider Base URL 无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("Provider Base URL 仅支持 http 或 https")
	}
	if u.User != nil {
		return fmt.Errorf("Provider Base URL 不允许包含用户信息")
	}
	if p.TimeoutS < 1 || p.TimeoutS > 300 {
		return fmt.Errorf("Provider 超时需为 1 至 300 秒")
	}
	if p.BatchSize < 1 || p.BatchSize > 50 {
		return fmt.Errorf("Provider 单批工具数需为 1 至 50")
	}
	if p.MaxConcurrency < 1 || p.MaxConcurrency > 3 {
		return fmt.Errorf("Provider 最大并发数需为 1 至 3")
	}
	return nil
}
