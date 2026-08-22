package risk

import "testing"

func TestValidateProvider(t *testing.T) {
	valid := Provider{Name: "local", BaseURL: "http://127.0.0.1:11434/v1", APIStyle: APIStyleChatCompletions, Model: "model", Enabled: true, TimeoutS: 60, BatchSize: 10, MaxConcurrency: 1}
	if err := ValidateProvider(valid); err != nil {
		t.Fatalf("有效配置被拒绝: %v", err)
	}
	for _, raw := range []string{"file:///tmp/model", "https://user:pass@example.com/v1", "ftp://example.com"} {
		invalid := valid
		invalid.BaseURL = raw
		if err := ValidateProvider(invalid); err == nil {
			t.Errorf("危险 URL %q 应被拒绝", raw)
		}
	}
	invalid := valid
	invalid.MaxConcurrency = 4
	if err := ValidateProvider(invalid); err == nil {
		t.Fatal("并发数超过 3 应被拒绝")
	}
}
