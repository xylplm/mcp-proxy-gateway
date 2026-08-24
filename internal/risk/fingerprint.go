package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func ToolFingerprint(tool domain.ToolDef) (string, error) {
	var schema any
	if len(tool.InputSchema) == 0 {
		schema = map[string]any{}
	} else if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		return "", fmt.Errorf("规范化工具 Schema 失败: %w", err)
	}
	payload := struct {
		UpstreamID   string `json:"upstream_id"`
		OriginalName string `json:"original_name"`
		Description  string `json:"description"`
		InputSchema  any    `json:"input_schema"`
	}{
		UpstreamID: tool.UpstreamID, OriginalName: tool.OriginalName,
		Description: tool.Description, InputSchema: schema,
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("编码工具指纹失败: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
