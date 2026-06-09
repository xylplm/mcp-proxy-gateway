package transport

import (
	"net/http"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func TestValidateConnParamsAcceptsOptionalEnvCWDAndHeaders(t *testing.T) {
	cases := []domain.UpstreamConfig{
		{
			Name:      "stdio-extra",
			Transport: domain.TransportStdio,
			ConnParams: map[string]any{
				ParamCommand: "npx",
				ParamArgs:    []any{"-y", "server"},
				ParamEnv:     map[string]any{"TOKEN": "secret", "MODE": "readonly"},
				ParamCWD:     "/tmp",
			},
		},
		{
			Name:      "http-extra",
			Transport: domain.TransportStreamableHTTP,
			ConnParams: map[string]any{
				ParamURL:     "https://example.com/mcp",
				ParamHeaders: map[string]any{"Authorization": "Bearer token", "X-Team": "dev"},
			},
		},
		{
			Name:      "ws-extra",
			Transport: domain.TransportWebSocket,
			ConnParams: map[string]any{
				ParamURL:     "wss://example.com/mcp",
				ParamHeaders: map[string]string{"X-API-Key": "key"},
			},
		},
	}

	for _, cfg := range cases {
		if err := ValidateConnParams(cfg); err != nil {
			t.Fatalf("%s should accept optional params: %v", cfg.Name, err)
		}
	}
}

func TestValidateConnParamsRejectsInvalidOptionalMaps(t *testing.T) {
	cases := []struct {
		name      string
		cfg       domain.UpstreamConfig
		wantField string
	}{
		{
			name: "stdio env non-string value",
			cfg: domain.UpstreamConfig{
				Transport: domain.TransportStdio,
				ConnParams: map[string]any{
					ParamCommand: "npx",
					ParamEnv:     map[string]any{"TOKEN": 123},
				},
			},
			wantField: "connParams.env",
		},
		{
			name: "http headers non-map",
			cfg: domain.UpstreamConfig{
				Transport: domain.TransportSSE,
				ConnParams: map[string]any{
					ParamURL:     "https://example.com/sse",
					ParamHeaders: "Authorization: Bearer token",
				},
			},
			wantField: "connParams.headers",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConnParams(tc.cfg)
			if err == nil {
				t.Fatal("expected validation error")
			}
			apiErr, ok := err.(*domain.APIError)
			if !ok {
				t.Fatalf("expected *domain.APIError, got %T", err)
			}
			if _, ok := apiErr.Fields[tc.wantField]; !ok {
				t.Fatalf("expected field %s, got %v", tc.wantField, apiErr.Fields)
			}
		})
	}
}

func TestParseConnParamsKeepsOptionalParams(t *testing.T) {
	cfg := domain.UpstreamConfig{
		Transport: domain.TransportStdio,
		ConnParams: map[string]any{
			ParamCommand: "npx",
			ParamArgs:    []any{"-y", "server"},
			ParamEnv:     map[string]any{"TOKEN": "secret"},
			ParamCWD:     "/workspace",
		},
	}

	params, err := parseConnParams(cfg)
	if err != nil {
		t.Fatalf("parseConnParams failed: %v", err)
	}
	if params.command != "npx" || params.cwd != "/workspace" {
		t.Fatalf("unexpected command/cwd: %+v", params)
	}
	if len(params.args) != 2 || params.args[1] != "server" {
		t.Fatalf("unexpected args: %+v", params.args)
	}
	if params.env["TOKEN"] != "secret" {
		t.Fatalf("unexpected env: %+v", params.env)
	}
}

func TestAuthHTTPClientMergesHeadersAndCredential(t *testing.T) {
	rt := &authRoundTripper{
		credential: "cred-token",
		headers: map[string]string{
			"X-API-Key": "api-key",
		},
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("X-API-Key") != "api-key" {
				t.Fatalf("missing custom header: %v", req.Header)
			}
			if req.Header.Get("Authorization") != "Bearer cred-token" {
				t.Fatalf("unexpected authorization: %q", req.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestAuthHTTPClientKeepsExplicitAuthorization(t *testing.T) {
	rt := &authRoundTripper{
		credential: "cred-token",
		headers: map[string]string{
			"Authorization": "ApiKey explicit",
		},
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Authorization") != "ApiKey explicit" {
				t.Fatalf("explicit authorization should win, got %q", req.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
		}),
	}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	_ = resp.Body.Close()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
