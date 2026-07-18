package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestFSBrowseRootsOK(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})
	w := doJSON(e, http.MethodGet, "/api/admin/fs/roots", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	raw := envelopeData(t, w)
	var data struct {
		Roots []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"roots"`
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Roots) == 0 || data.Platform == "" {
		t.Fatalf("%+v", data)
	}
}

func TestFSBrowseListValidationAndOK(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})

	w := doJSON(e, http.MethodGet, "/api/admin/fs/list", "")
	if w.Code == http.StatusOK {
		t.Fatal("missing path should fail")
	}

	w = doJSON(e, http.MethodGet, "/api/admin/fs/list?path="+url.QueryEscape("/data/ws"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSON(e, http.MethodGet, "/api/admin/fs/list?path="+url.QueryEscape("/denied"), "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}

	w = doJSON(e, http.MethodGet, "/api/admin/fs/list?path="+url.QueryEscape("/missing"), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestFSBrowseStatAndUnavailable(t *testing.T) {
	e := newTestEngine(Deps{RuntimeEnv: &fakeRuntimeEnv{}})
	w := doJSON(e, http.MethodGet, "/api/admin/fs/stat?path="+url.QueryEscape("/data"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	e = newTestEngine(Deps{})
	w = doJSON(e, http.MethodGet, "/api/admin/fs/roots", "")
	if w.Code == http.StatusOK {
		t.Fatal("expected unavailable without runtime env")
	}
}
