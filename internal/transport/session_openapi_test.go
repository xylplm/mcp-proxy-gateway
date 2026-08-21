package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const testOpenAPIDoc = `
openapi: 3.0.3
info:
  title: Pet API
  version: 1.0.0
paths:
  /pets/{id}:
    get:
      operationId: getPet
      summary: Get a pet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: verbose
          in: query
          schema:
            type: boolean
    patch:
      operationId: updatePet
      summary: Update a pet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
`

func TestOpenAPISessionListsToolsFromDocument(t *testing.T) {
	sess := mustOpenAPISession(t, "https://api.example.test/v1", testOpenAPIDoc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != "getPet" || tools[1].Name != "updatePet" {
		t.Fatalf("unexpected tool names: %+v", tools)
	}
	var schema struct {
		Required   []string               `json:"required"`
		Properties map[string]interface{} `json:"properties"`
	}
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema should be JSON: %v", err)
	}
	if _, ok := schema.Properties["id"]; !ok {
		t.Fatalf("path parameter should be exposed in input schema: %s", string(tools[0].InputSchema))
	}
}

func TestOpenAPISessionCallsHTTPWithPathQueryBodyAndAuth(t *testing.T) {
	var gotMethod, gotPath, gotVerbose, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		gotVerbose = r.URL.Query().Get("verbose")
		gotAuth = r.Header.Get("Authorization")
		body, _ := ioReadAllString(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	sess := mustOpenAPISession(t, server.URL+"/v1", testOpenAPIDoc)
	cfg := sess.(*openAPISession)
	cfg.params.openapi.authType = "bearer"
	cfg.params.openapi.authValue = "${credential}"
	cfg.credential = "secret-token"
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(context.Background(), "updatePet", json.RawMessage(`{"id":"p 1","body":{"name":"Milo"}}`))
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("2xx response should not be an error: %s", string(res.Content))
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/pets/p%201" || gotVerbose != "" {
		t.Fatalf("unexpected request target: method=%s path=%s verbose=%s", gotMethod, gotPath, gotVerbose)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"name":"Milo"`) {
		t.Fatalf("unexpected body: %s", gotBody)
	}
}

func TestOpenAPISessionReturnsHTTPErrorAsToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	sess := mustOpenAPISession(t, server.URL, testOpenAPIDoc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(context.Background(), "getPet", json.RawMessage(`{"id":"missing"}`))
	if err != nil {
		t.Fatalf("HTTP non-2xx should be returned as tool result, got error: %v", err)
	}
	if !res.IsError || !strings.Contains(string(res.Content), "not found") {
		t.Fatalf("expected tool error with response body, got: %+v", res)
	}
}

func TestValidateConnParamsAcceptsOpenAPI(t *testing.T) {
	cfg := domain.UpstreamConfig{
		Transport: domain.TransportOpenAPI,
		ConnParams: map[string]any{
			ParamOpenAPIBaseURL:    "https://api.example.com/v1",
			ParamOpenAPIDocContent: testOpenAPIDoc,
			ParamOpenAPIAuthType:   "api-key-header",
			ParamOpenAPIAuthName:   "X-API-Key",
			ParamOpenAPIAuthValue:  "${credential}",
		},
	}
	if err := ValidateConnParams(cfg); err != nil {
		t.Fatalf("openapi params should be valid: %v", err)
	}
}

func TestOpenAPISessionSupportsPathParametersAndLocalRefs(t *testing.T) {
	doc := `
openapi: 3.0.3
info:
  title: Ref API
  version: 1.0.0
components:
  schemas:
    CreateTodo:
      type: object
      properties:
        title:
          type: string
  parameters:
    TeamID:
      name: teamId
      in: path
      required: true
      schema:
        type: string
paths:
  /teams/{teamId}/todos:
    parameters:
      - $ref: '#/components/parameters/TeamID'
    post:
      operationId: createTodo
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateTodo'
`
	sess := mustOpenAPISession(t, "https://api.example.test", doc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "createTodo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	if !strings.Contains(string(tools[0].InputSchema), "teamId") || !strings.Contains(string(tools[0].InputSchema), "title") {
		t.Fatalf("schema should include path parameter and resolved request body: %s", string(tools[0].InputSchema))
	}
}

func TestOpenAPISessionEscapesPathParameters(t *testing.T) {
	var gotRequestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	sess := mustOpenAPISession(t, server.URL, testOpenAPIDoc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	if _, err := sess.CallTool(context.Background(), "getPet", json.RawMessage(`{"id":"a/b c"}`)); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if gotRequestURI != "/pets/a%2Fb%20c" {
		t.Fatalf("path parameter should be escaped in request URI, got %q", gotRequestURI)
	}
}

func TestOpenAPIConnBuildRequestPreservesEscapedPathParameters(t *testing.T) {
	sess := mustOpenAPISession(t, "https://api.example.test", testOpenAPIDoc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	raw := sess.(*openAPISession).conn
	conn, ok := raw.(*openAPIConn)
	if !ok {
		t.Fatalf("expected openAPIConn, got %T", raw)
	}
	req, err := conn.buildRequest(context.Background(), conn.operations["getPet"], map[string]any{"id": "a/b c"})
	if err != nil {
		t.Fatalf("buildRequest failed: %v", err)
	}
	if req.URL.RequestURI() != "/pets/a%2Fb%20c" {
		t.Fatalf("path parameter should be escaped before dispatch, got uri=%q rawPath=%q path=%q", req.URL.RequestURI(), req.URL.RawPath, req.URL.Path)
	}
}

func TestOpenAPISessionRequiresDeclaredRequiredParameters(t *testing.T) {
	doc := `
openapi: 3.0.3
info:
  title: Required API
  version: 1.0.0
paths:
  /reports:
    get:
      operationId: listReports
      parameters:
        - name: tenant
          in: query
          required: true
          schema:
            type: string
`
	sess := mustOpenAPISession(t, "https://api.example.test", doc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	_, err := sess.CallTool(context.Background(), "listReports", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("missing required query parameter should fail clearly, got %v", err)
	}
}

func TestOpenAPISessionRejectsUnsupportedDocumentVersion(t *testing.T) {
	doc := `
swagger: "2.0"
info:
  title: Legacy API
  version: 1.0.0
paths:
  /ping:
    get:
      operationId: ping
`
	_, err := loadOpenAPIDocument(context.Background(), http.DefaultClient, openAPIParams{docContent: doc})
	if err == nil || !strings.Contains(err.Error(), "OpenAPI") {
		t.Fatalf("Swagger 2.0 document should be rejected, got %v", err)
	}
}

func TestOpenAPIDocumentRejectsOversizedRemoteDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", openAPIMaxDocumentBytes+1)))
	}))
	defer server.Close()

	_, err := loadOpenAPIDocument(context.Background(), server.Client(), openAPIParams{docURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "4MB") {
		t.Fatalf("oversized OpenAPI document should be rejected clearly, got %v", err)
	}
}

func TestOpenAPISessionHandlesRecursiveSchemaRef(t *testing.T) {
	doc := `
openapi: 3.0.3
info:
  title: Recursive API
  version: 1.0.0
components:
  schemas:
    Loop:
      $ref: '#/components/schemas/Loop'
paths:
  /loops:
    post:
      operationId: createLoop
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Loop'
`
	sess := mustOpenAPISession(t, "https://api.example.test", doc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect should not fail or recurse forever on recursive refs: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || !json.Valid(tools[0].InputSchema) {
		t.Fatalf("recursive ref should still produce one valid tool schema: %+v", tools)
	}
}

func TestOpenAPISessionResolvesNestedSchemaRefs(t *testing.T) {
	doc := `
openapi: 3.0.3
info:
  title: Nested Ref API
  version: 1.0.0
components:
  schemas:
    Todo:
      type: object
      properties:
        owner:
          $ref: '#/components/schemas/User'
        tags:
          type: array
          items:
            $ref: '#/components/schemas/Tag'
    User:
      type: object
      properties:
        email:
          type: string
    Tag:
      type: object
      properties:
        name:
          type: string
paths:
  /todos:
    post:
      operationId: createNestedTodo
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Todo'
`
	sess := mustOpenAPISession(t, "https://api.example.test", doc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	schema := string(tools[0].InputSchema)
	if strings.Contains(schema, `"$ref"`) || !strings.Contains(schema, `"email"`) || !strings.Contains(schema, `"name"`) {
		t.Fatalf("nested schema refs should be resolved for tool preview: %s", schema)
	}
}

func TestOpenAPISessionSkipsUnsupportedParameterLocations(t *testing.T) {
	doc := `
openapi: 3.0.3
info:
  title: Callback API
  version: 1.0.0
paths:
  /events:
    post:
      operationId: createEvent
      parameters:
        - name: callback
          in: callback
          required: true
          schema:
            type: string
        - name: X-Trace
          in: header
          schema:
            type: string
`
	sess := mustOpenAPISession(t, "https://api.example.test", doc)
	if err := sess.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	schema := string(tools[0].InputSchema)
	if strings.Contains(schema, "callback") || !strings.Contains(schema, "X-Trace") {
		t.Fatalf("schema should only expose supported parameter locations: %s", schema)
	}
}

func mustOpenAPISession(t *testing.T, baseURL string, doc string) UpstreamSession {
	t.Helper()
	sess, err := NewFactory().NewSession(domain.UpstreamConfig{
		Name:       "openapi",
		Transport:  domain.TransportOpenAPI,
		ConnParams: map[string]any{ParamOpenAPIBaseURL: baseURL, ParamOpenAPIDocContent: doc},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	return sess
}

func ioReadAllString(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	return string(b), err
}

// 工具结果会进入模型上下文与调用记录，上游响应头里的会话凭证必须先脱敏。
func TestOpenAPIResultRedactsSensitiveResponseHeaders(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header: http.Header{
			"Set-Cookie":   []string{"session=super-secret; HttpOnly"},
			"Content-Type": []string{"application/json"},
		},
	}
	content := marshalOpenAPIResultContent(resp, []byte(`{"ok":true}`), false)
	if strings.Contains(string(content), "super-secret") {
		t.Fatalf("响应头中的凭证不应出现在工具结果里：%s", content)
	}
	if !strings.Contains(string(content), "redacted") {
		t.Fatalf("敏感响应头应保留键名并标记脱敏：%s", content)
	}
	// 非敏感头需要照常保留，否则会失去排查价值。
	if !strings.Contains(string(content), "application/json") {
		t.Fatalf("普通响应头不应被移除：%s", content)
	}
}
