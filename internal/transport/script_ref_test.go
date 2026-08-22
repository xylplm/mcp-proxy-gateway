package transport

import (
	"maps"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
)

func TestResolveManagedScriptAndHash(t *testing.T) {
	dir := t.TempDir()
	svc := scripts.NewService(dir, nil)
	detail, err := svc.Create(scripts.CreateInput{
		Name: "managed-demo", Language: scripts.LangPython, Content: "print(1)\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	SetScriptService(svc)
	t.Cleanup(func() { SetScriptService(nil) })

	params := map[string]any{
		ParamLaunchMode: "script",
		ParamScriptRef: map[string]any{
			"scriptId":      detail.ID,
			"version":       detail.CurrentVersion,
			"contentSha256": detail.ContentSHA256,
		},
		// 客户端夹带内容应被忽略
		ParamCommand: "bash",
		ParamArgs:    []any{"evil"},
		ParamCWD:     "/tmp",
	}
	cmd, args, cwd, risk, _, ok, err := resolveManagedScript(params)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if cmd != "python3" || len(args) != 1 || cwd == "" || risk == "" {
		t.Fatalf("cmd=%s args=%v cwd=%s risk=%s", cmd, args, cwd, risk)
	}

	params[ParamScriptRef].(map[string]any)["contentSha256"] = "bad"
	if _, _, _, _, _, _, err := resolveManagedScript(params); err == nil {
		t.Fatal("bad hash should fail")
	}
}

func TestScriptRefRequiresImmutableVersionAndHash(t *testing.T) {
	base := map[string]any{
		"scriptId":      "scr_0123456789abcdef",
		"version":       "v1",
		"contentSha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	for name, mutate := range map[string]func(map[string]any){
		"missing version": func(m map[string]any) { delete(m, "version") },
		"current version": func(m map[string]any) { m["version"] = "current" },
		"missing hash":    func(m map[string]any) { delete(m, "contentSha256") },
		"invalid hash":    func(m map[string]any) { m["contentSha256"] = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			ref := make(map[string]any, len(base))
			maps.Copy(ref, base)
			mutate(ref)
			_, _, err := scriptRefFromParams(map[string]any{
				ParamLaunchMode: "script",
				ParamScriptRef:  ref,
			})
			if err == nil {
				t.Fatal("invalid immutable binding should fail")
			}
		})
	}
}

func TestScriptRefRequiresService(t *testing.T) {
	SetScriptService(nil)
	params := map[string]any{
		ParamLaunchMode: "script",
		ParamScriptRef: map[string]any{
			"scriptId":      "scr_0123456789abcdef",
			"version":       "v1",
			"contentSha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}
	if _, _, _, _, _, ok, err := resolveManagedScript(params); !ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
