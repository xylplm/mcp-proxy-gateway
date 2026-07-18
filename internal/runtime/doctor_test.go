package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorProbeAndSummary(t *testing.T) {
	t.Parallel()
	d := NewDoctor(func(file string) (string, error) {
		switch file {
		case "node", "npx":
			return "/usr/bin/" + file, nil
		default:
			return "", fmt.Errorf("not found")
		}
	})
	tools := d.Probe()
	if len(tools) != len(DefaultProbeTools()) {
		t.Fatalf("tool count=%d", len(tools))
	}
	sum := BuildSummary(DefaultPolicy(), tools, "/data", "/data/runtime", []string{"/data/runtime/bin"})
	if sum.AvailableCount != 2 {
		t.Fatalf("available=%d", sum.AvailableCount)
	}
	if sum.MissingCount != len(tools)-2 {
		t.Fatalf("missing=%d", sum.MissingCount)
	}
	if sum.DataDir != "/data" {
		t.Fatalf("dataDir=%q", sum.DataDir)
	}
	if sum.RuntimeDir != "/data/runtime" {
		t.Fatalf("runtimeDir=%q", sum.RuntimeDir)
	}
	if len(sum.PathPrefixes) != 1 {
		t.Fatalf("pathPrefixes=%v", sum.PathPrefixes)
	}
	if !sum.StdioEnabled {
		t.Fatal("stdio should be enabled")
	}
	if len(sum.CommandAllowlist) == 0 {
		t.Fatal("allowlist should be present for default policy")
	}
	foundGuide := false
	for _, n := range sum.RiskNotes {
		if strings.Contains(n, "/data/runtime") {
			foundGuide = true
			break
		}
	}
	if !foundGuide {
		t.Fatalf("expected runtime guide in notes: %v", sum.RiskNotes)
	}
}

func TestServiceSummaryCreatesLayout(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	rt := filepath.Join(data, "runtime")
	svc := NewService(
		func() Policy { return DefaultPolicy() },
		func() string { return data },
		func() string { return rt },
	)
	sum := svc.Summary()
	if sum.RuntimeDir != rt {
		t.Fatalf("runtimeDir=%q", sum.RuntimeDir)
	}
	if !sum.LayoutReady {
		t.Fatal("layout should be ready after Summary")
	}
	if _, err := os.Stat(filepath.Join(rt, "bin")); err != nil {
		t.Fatalf("bin not created: %v", err)
	}
}
