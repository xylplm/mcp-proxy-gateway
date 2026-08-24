package risk

import (
	"encoding/json"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func TestToolFingerprintCanonicalJSON(t *testing.T) {
	a := domain.ToolDef{UpstreamID: "up-1", OriginalName: "search", Description: "查找", InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}`)}
	b := a
	b.InputSchema = json.RawMessage(`{"properties":{"b":{"type":"number"},"a":{"type":"string"}},"type":"object"}`)
	c := a
	c.InputSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"boolean"},"b":{"type":"number"}}}`)

	fa, err := ToolFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := ToolFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	fc, err := ToolFingerprint(c)
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb {
		t.Fatalf("仅 JSON 键顺序变化不应改变指纹: %s != %s", fa, fb)
	}
	if fa == fc {
		t.Fatal("Schema 实质变化必须改变指纹")
	}
}
