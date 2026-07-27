package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/scripts"
)

type warningScriptService struct {
	ScriptService
}

func (warningScriptService) Delete(string) error {
	return fmt.Errorf("%w: simulated trash move failure", scripts.ErrTrashMoveFailed)
}

func TestDeleteScriptReturnsCleanupWarning(t *testing.T) {
	e := newTestEngine(Deps{Scripts: warningScriptService{}})
	w := doJSON(e, http.MethodDelete, "/api/admin/scripts/script-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var data map[string]any
	if err := json.Unmarshal(envelopeData(t, w), &data); err != nil {
		t.Fatal(err)
	}
	if data["deleted"] != true || data["warning"] == "" {
		t.Fatalf("response=%+v, want deleted=true with warning", data)
	}
}

func TestScriptsCRUDFlow(t *testing.T) {
	dir := t.TempDir()
	svc := scripts.NewService(dir)
	e := newTestEngine(Deps{Scripts: svc})

	// create
	w := doJSON(e, http.MethodPost, "/api/admin/scripts", `{
		"name":"hello-py",
		"language":"python",
		"content":"print(1)\n"
	}`)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		// respondCreated uses 201
		if w.Code != 201 {
			t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
		}
	}
	raw := envelopeData(t, w)
	var created scripts.ScriptDetail
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("missing id")
	}

	// list
	w = doJSON(e, http.MethodGet, "/api/admin/scripts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}

	// get
	w = doJSON(e, http.MethodGet, "/api/admin/scripts/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get %d %s", w.Code, w.Body.String())
	}

	// save content
	w = doJSON(e, http.MethodPut, "/api/admin/scripts/"+created.ID+"/content", `{"content":"print(2)\n","note":"v2"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}

	// launch binding
	w = doJSON(e, http.MethodPost, "/api/admin/scripts/"+created.ID+"/launch", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("launch %d %s", w.Code, w.Body.String())
	}
	raw = envelopeData(t, w)
	var launch map[string]any
	if err := json.Unmarshal(raw, &launch); err != nil {
		t.Fatal(err)
	}
	if launch["command"] != "python3" {
		t.Fatalf("%+v", launch)
	}

	// analyze
	w = doJSON(e, http.MethodPost, "/api/admin/scripts/analyze", `{"content":"import subprocess\n"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("analyze %d %s", w.Code, w.Body.String())
	}

	// delete
	w = doJSON(e, http.MethodDelete, "/api/admin/scripts/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
}

type fakeScriptUpstreamService struct {
	items []domain.Upstream
}

func (f *fakeScriptUpstreamService) Create(context.Context, domain.UpstreamConfig) (domain.Upstream, error) {
	return domain.Upstream{}, nil
}
func (f *fakeScriptUpstreamService) Update(context.Context, string, domain.UpstreamConfig) (domain.Upstream, error) {
	return domain.Upstream{}, nil
}
func (f *fakeScriptUpstreamService) Delete(context.Context, string) error { return nil }
func (f *fakeScriptUpstreamService) List(context.Context) ([]domain.Upstream, error) {
	return f.items, nil
}
func (f *fakeScriptUpstreamService) SetEnabled(context.Context, string, bool) error { return nil }
func (f *fakeScriptUpstreamService) Reorder(context.Context, []string) error        { return nil }
func (f *fakeScriptUpstreamService) Reconnect(context.Context, string) error        { return nil }

func TestScriptLaunchOptionalBodyValidation(t *testing.T) {
	dir := t.TempDir()
	svc := scripts.NewService(dir)
	detail, err := svc.Create(scripts.CreateInput{Name: "launch-body", Language: scripts.LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(Deps{Scripts: svc})
	for name, body := range map[string]string{
		"empty":  "",
		"object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := doJSON(e, http.MethodPost, "/api/admin/scripts/"+detail.ID+"/launch", body)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for name, body := range map[string]string{
		"truncated":  `{`,
		"wrong type": `[]`,
		"trailing":   `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := doJSON(e, http.MethodPost, "/api/admin/scripts/"+detail.ID+"/launch", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeleteScriptRejectsReferenced(t *testing.T) {
	dir := t.TempDir()
	svc := scripts.NewService(dir)
	detail, err := svc.Create(scripts.CreateInput{Name: "ref-demo", Language: scripts.LangPython, Content: "print(1)\n"})
	if err != nil {
		t.Fatal(err)
	}
	upstream := &fakeScriptUpstreamService{items: []domain.Upstream{{
		Config: domain.UpstreamConfig{
			Name: "using-script", Transport: domain.TransportStdio, ConnParams: map[string]any{
				"launchMode": "script",
				"scriptRef":  map[string]any{"scriptId": detail.ID},
			},
		},
	}}}
	e := newTestEngine(Deps{Scripts: svc, Upstream: upstream})
	w := doJSON(e, http.MethodDelete, "/api/admin/scripts/"+detail.ID, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestScriptsUnavailable(t *testing.T) {
	e := newTestEngine(Deps{})
	w := doJSON(e, http.MethodGet, "/api/admin/scripts", "")
	if w.Code == http.StatusOK {
		t.Fatal("expected unavailable")
	}
}
