package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

const (
	openAPIDefaultContentType = "application/json"
	openAPIMaxResponseBytes   = 1 << 20
)

var openAPITemplateParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

type openAPISession struct {
	*baseSession
}

func newOpenAPISession(cfg domain.UpstreamConfig) (UpstreamSession, error) {
	base, err := newBaseSession(cfg, connectTimeoutOf(cfg))
	if err != nil {
		return nil, err
	}
	return &openAPISession{baseSession: base}, nil
}

func (s *openAPISession) Connect(ctx context.Context) error {
	return s.establish(ctx, func(dialCtx context.Context) (mcpClientConn, error) {
		client := &http.Client{}
		doc, err := loadOpenAPIDocument(dialCtx, client, s.params.openapi)
		if err != nil {
			return nil, err
		}
		tools, operations, err := compileOpenAPIOperations(doc)
		if err != nil {
			return nil, err
		}
		return &openAPIConn{
			params:     s.params.openapi,
			credential: s.credential,
			client:     client,
			tools:      tools,
			operations: operations,
		}, nil
	})
}

type openAPIConn struct {
	params     openAPIParams
	credential string
	client     *http.Client
	tools      []domain.ToolDef
	operations map[string]openAPIOperation
}

func (c *openAPIConn) listTools(context.Context) ([]domain.ToolDef, error) {
	out := make([]domain.ToolDef, len(c.tools))
	copy(out, c.tools)
	return out, nil
}

func (c *openAPIConn) callTool(ctx context.Context, name string, args json.RawMessage) (domain.ToolResult, error) {
	op, ok := c.operations[name]
	if !ok {
		return domain.ToolResult{}, domain.NewError(domain.CodeNotFound, "OpenAPI 工具不存在："+name)
	}
	var values map[string]any
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &values); err != nil {
			return domain.ToolResult{}, domain.NewError(domain.CodeValidation, "工具入参必须是 JSON 对象："+err.Error())
		}
	} else {
		values = map[string]any{}
	}

	req, err := c.buildRequest(ctx, op, values)
	if err != nil {
		return domain.ToolResult{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, "调用 OpenAPI 服务失败："+err.Error())
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, openAPIMaxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return domain.ToolResult{}, domain.NewError(domain.CodeUpstreamUnavailable, "读取 OpenAPI 响应失败："+err.Error())
	}
	truncated := len(body) > openAPIMaxResponseBytes
	if truncated {
		body = body[:openAPIMaxResponseBytes]
	}
	content := marshalOpenAPIResultContent(resp, body, truncated)
	return domain.ToolResult{IsError: resp.StatusCode < 200 || resp.StatusCode >= 300, Content: content}, nil
}

func (c *openAPIConn) close() error {
	return nil
}

func (c *openAPIConn) buildRequest(ctx context.Context, op openAPIOperation, values map[string]any) (*http.Request, error) {
	base, err := url.Parse(c.params.baseURL)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "OpenAPI baseUrl 非法："+err.Error())
	}
	path, err := expandOpenAPIPath(op.path, values, op.parameters)
	if err != nil {
		return nil, err
	}
	endpoint := joinOpenAPIEndpoint(base, path)
	query := endpoint.Query()
	headers := make(http.Header)
	for k, v := range c.params.headers {
		headers.Set(k, resolveCredentialPlaceholders(v, c.credential))
	}
	var body io.Reader

	for _, param := range op.parameters {
		value, exists := values[param.Name]
		if !exists || param.In == "path" {
			if !exists && param.Required && param.In != "path" {
				return nil, domain.NewError(domain.CodeValidation, "缺少必填 "+openAPIParamLocationLabel(param.In)+" 参数："+param.Name)
			}
			continue
		}
		rendered := openAPIStringValue(value)
		if rendered == "" {
			if param.Required {
				return nil, domain.NewError(domain.CodeValidation, "缺少必填 "+openAPIParamLocationLabel(param.In)+" 参数："+param.Name)
			}
			continue
		}
		switch param.In {
		case "query":
			query.Set(param.Name, rendered)
		case "header":
			headers.Set(param.Name, rendered)
		case "cookie":
			reqCookie := &http.Cookie{Name: param.Name, Value: rendered}
			headers.Add("Cookie", reqCookie.String())
		}
	}
	if op.hasBody {
		rawBody, ok := values["body"]
		if !ok {
			if op.bodyRequired {
				return nil, domain.NewError(domain.CodeValidation, "缺少必填请求体参数 body")
			}
		} else {
			payload, err := json.Marshal(rawBody)
			if err != nil {
				return nil, domain.NewError(domain.CodeValidation, "序列化请求体失败："+err.Error())
			}
			body = bytes.NewReader(payload)
			if headers.Get("Content-Type") == "" {
				headers.Set("Content-Type", openAPIDefaultContentType)
			}
		}
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(op.method), endpoint.String(), body)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "构造 OpenAPI 请求失败："+err.Error())
	}
	req.Header = headers
	applyOpenAPIAuth(req, c.params, c.credential)
	return req, nil
}

type openAPIDocument struct {
	OpenAPI    string                     `json:"openapi" yaml:"openapi"`
	Info       openAPIInfo                `json:"info" yaml:"info"`
	Paths      map[string]openAPIPathItem `json:"paths" yaml:"paths"`
	Components openAPIComponents          `json:"components" yaml:"components"`
}

type openAPIInfo struct {
	Title string `json:"title" yaml:"title"`
}

type openAPIComponents struct {
	Schemas       map[string]any                `json:"schemas" yaml:"schemas"`
	Parameters    map[string]openAPIParameter   `json:"parameters" yaml:"parameters"`
	RequestBodies map[string]openAPIRequestBody `json:"requestBodies" yaml:"requestBodies"`
}

type openAPIPathItem struct {
	Parameters []openAPIParameter `json:"parameters" yaml:"parameters"`
	Get        *openAPIOper       `json:"get" yaml:"get"`
	Post       *openAPIOper       `json:"post" yaml:"post"`
	Put        *openAPIOper       `json:"put" yaml:"put"`
	Patch      *openAPIOper       `json:"patch" yaml:"patch"`
	Delete     *openAPIOper       `json:"delete" yaml:"delete"`
	Head       *openAPIOper       `json:"head" yaml:"head"`
	Options    *openAPIOper       `json:"options" yaml:"options"`
}

func (p openAPIPathItem) operations() map[string]openAPIOper {
	ops := make(map[string]openAPIOper)
	if p.Get != nil {
		ops["get"] = *p.Get
	}
	if p.Post != nil {
		ops["post"] = *p.Post
	}
	if p.Put != nil {
		ops["put"] = *p.Put
	}
	if p.Patch != nil {
		ops["patch"] = *p.Patch
	}
	if p.Delete != nil {
		ops["delete"] = *p.Delete
	}
	if p.Head != nil {
		ops["head"] = *p.Head
	}
	if p.Options != nil {
		ops["options"] = *p.Options
	}
	return ops
}

type openAPIOper struct {
	OperationID string              `json:"operationId" yaml:"operationId"`
	Summary     string              `json:"summary" yaml:"summary"`
	Description string              `json:"description" yaml:"description"`
	Parameters  []openAPIParameter  `json:"parameters" yaml:"parameters"`
	RequestBody *openAPIRequestBody `json:"requestBody" yaml:"requestBody"`
	Deprecated  bool                `json:"deprecated" yaml:"deprecated"`
}

type openAPIParameter struct {
	Ref         string `json:"$ref" yaml:"$ref"`
	Name        string `json:"name" yaml:"name"`
	In          string `json:"in" yaml:"in"`
	Required    bool   `json:"required" yaml:"required"`
	Description string `json:"description" yaml:"description"`
	Schema      any    `json:"schema" yaml:"schema"`
}

type openAPIRequestBody struct {
	Ref         string                      `json:"$ref" yaml:"$ref"`
	Required    bool                        `json:"required" yaml:"required"`
	Description string                      `json:"description" yaml:"description"`
	Content     map[string]openAPIMediaType `json:"content" yaml:"content"`
}

type openAPIMediaType struct {
	Schema any `json:"schema" yaml:"schema"`
}

type openAPIOperation struct {
	name         string
	method       string
	path         string
	parameters   []openAPIParameter
	hasBody      bool
	bodyRequired bool
	bodySchema   any
	bodyDesc     string
}

type openAPIExpandedPath struct {
	path    string
	rawPath string
}

func loadOpenAPIDocument(ctx context.Context, client *http.Client, params openAPIParams) (openAPIDocument, error) {
	raw := strings.TrimSpace(params.docContent)
	if raw == "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(params.docURL), nil)
		if err != nil {
			return openAPIDocument{}, domain.NewError(domain.CodeValidation, "OpenAPI 文档地址非法："+err.Error())
		}
		resp, err := client.Do(req)
		if err != nil {
			return openAPIDocument{}, domain.NewError(domain.CodeUpstreamUnavailable, "拉取 OpenAPI 文档失败："+err.Error())
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return openAPIDocument{}, domain.NewError(domain.CodeUpstreamUnavailable, fmt.Sprintf("拉取 OpenAPI 文档失败：HTTP %d", resp.StatusCode))
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return openAPIDocument{}, domain.NewError(domain.CodeUpstreamUnavailable, "读取 OpenAPI 文档失败："+err.Error())
		}
		raw = string(b)
	}
	var doc openAPIDocument
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return openAPIDocument{}, domain.NewError(domain.CodeValidation, "解析 OpenAPI 文档失败："+err.Error())
	}
	version := strings.TrimSpace(doc.OpenAPI)
	if version == "" || len(doc.Paths) == 0 {
		return openAPIDocument{}, domain.NewError(domain.CodeValidation, "OpenAPI 文档缺少 openapi 或 paths")
	}
	if !strings.HasPrefix(version, "3.") {
		return openAPIDocument{}, domain.NewError(domain.CodeValidation, "仅支持 OpenAPI 3.x 文档")
	}
	return doc, nil
}

func compileOpenAPIOperations(doc openAPIDocument) ([]domain.ToolDef, map[string]openAPIOperation, error) {
	var tools []domain.ToolDef
	ops := make(map[string]openAPIOperation)
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pathItem := doc.Paths[path]
		pathParams := resolveOpenAPIParameters(pathItem.Parameters, doc)
		pathOps := pathItem.operations()
		methods := make([]string, 0, len(pathOps))
		for method := range pathOps {
			if isOpenAPIHTTPMethod(method) {
				methods = append(methods, method)
			}
		}
		sort.Strings(methods)
		for _, method := range methods {
			raw := pathOps[method]
			if raw.Deprecated {
				continue
			}
			op := compileOpenAPIOperation(method, path, raw, pathParams, doc)
			if _, exists := ops[op.name]; exists {
				op.name = uniqueOpenAPIToolName(op.name, ops)
			}
			schema, err := buildOpenAPIInputSchema(op)
			if err != nil {
				return nil, nil, err
			}
			desc := strings.TrimSpace(raw.Description)
			if desc == "" {
				desc = strings.TrimSpace(raw.Summary)
			}
			if desc == "" {
				desc = strings.ToUpper(method) + " " + path
			}
			tools = append(tools, domain.ToolDef{
				OriginalName: op.name,
				Name:         op.name,
				Description:  desc,
				InputSchema:  schema,
			})
			ops[op.name] = op
		}
	}
	if len(tools) == 0 {
		return nil, nil, domain.NewError(domain.CodeValidation, "OpenAPI 文档中没有可转换的 HTTP 操作")
	}
	return tools, ops, nil
}

func compileOpenAPIOperation(method, path string, raw openAPIOper, pathParams []openAPIParameter, doc openAPIDocument) openAPIOperation {
	op := openAPIOperation{
		name:       openAPIToolName(raw.OperationID, method, path),
		method:     strings.ToUpper(method),
		path:       path,
		parameters: mergeOpenAPIParameters(pathParams, resolveOpenAPIParameters(raw.Parameters, doc)),
	}
	if raw.RequestBody != nil {
		body := resolveOpenAPIRequestBody(*raw.RequestBody, doc)
		if media, ok := body.Content[openAPIDefaultContentType]; ok {
			op.hasBody = true
			op.bodyRequired = body.Required
			op.bodySchema = resolveOpenAPISchema(media.Schema, doc)
			op.bodyDesc = body.Description
		}
	}
	return op
}

func buildOpenAPIInputSchema(op openAPIOperation) (json.RawMessage, error) {
	properties := make(map[string]any)
	required := make([]string, 0)
	for _, param := range op.parameters {
		if param.Name == "" || !isOpenAPIParamLocation(param.In) {
			continue
		}
		schema := rawSchemaOrString(param.Schema)
		if param.Description != "" {
			schema["description"] = param.Description
		}
		properties[param.Name] = schema
		if param.Required || param.In == "path" {
			required = append(required, param.Name)
		}
	}
	if op.hasBody {
		bodySchema := rawSchemaOrObject(op.bodySchema)
		if op.bodyDesc != "" {
			bodySchema["description"] = op.bodyDesc
		}
		properties["body"] = bodySchema
		if op.bodyRequired {
			required = append(required, "body")
		}
	}
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "生成 OpenAPI 工具入参 Schema 失败："+err.Error())
	}
	return b, nil
}

func rawSchemaOrString(raw any) map[string]any {
	if raw == nil {
		return map[string]any{"type": "string"}
	}
	schema, ok := openAPISchemaMap(raw)
	if !ok {
		return map[string]any{"type": "string"}
	}
	return schema
}

func rawSchemaOrObject(raw any) map[string]any {
	if raw == nil {
		return map[string]any{"type": "object"}
	}
	schema, ok := openAPISchemaMap(raw)
	if !ok {
		return map[string]any{"type": "object"}
	}
	return schema
}

func openAPISchemaMap(raw any) (map[string]any, bool) {
	switch v := raw.(type) {
	case map[string]any:
		return normalizeOpenAPIMap(v), true
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[fmt.Sprint(key)] = normalizeOpenAPIValue(value)
		}
		return out, true
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(v, &out); err != nil || out == nil {
			return nil, false
		}
		return out, true
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil || out == nil {
			return nil, false
		}
		return out, true
	}
}

func resolveOpenAPIParameters(params []openAPIParameter, doc openAPIDocument) []openAPIParameter {
	out := make([]openAPIParameter, 0, len(params))
	for _, param := range params {
		if param.Ref != "" {
			if resolved, ok := doc.Components.Parameters[openAPIRefName(param.Ref)]; ok {
				param = resolved
			}
		}
		param.In = strings.ToLower(strings.TrimSpace(param.In))
		param.Schema = resolveOpenAPISchema(param.Schema, doc)
		out = append(out, param)
	}
	return out
}

func resolveOpenAPIRequestBody(body openAPIRequestBody, doc openAPIDocument) openAPIRequestBody {
	if body.Ref != "" {
		if resolved, ok := doc.Components.RequestBodies[openAPIRefName(body.Ref)]; ok {
			body = resolved
		}
	}
	for contentType, media := range body.Content {
		media.Schema = resolveOpenAPISchema(media.Schema, doc)
		body.Content[contentType] = media
	}
	return body
}

func resolveOpenAPISchema(schema any, doc openAPIDocument) any {
	m, ok := openAPISchemaMap(schema)
	if !ok {
		return schema
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		if resolved, ok := doc.Components.Schemas[openAPIRefName(ref)]; ok {
			return resolveOpenAPISchema(resolved, doc)
		}
	}
	return m
}

func openAPIRefName(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func mergeOpenAPIParameters(pathParams []openAPIParameter, opParams []openAPIParameter) []openAPIParameter {
	out := make([]openAPIParameter, 0, len(pathParams)+len(opParams))
	seen := make(map[string]int)
	for _, param := range pathParams {
		key := param.In + ":" + param.Name
		seen[key] = len(out)
		out = append(out, param)
	}
	for _, param := range opParams {
		key := param.In + ":" + param.Name
		if idx, ok := seen[key]; ok {
			out[idx] = param
			continue
		}
		seen[key] = len(out)
		out = append(out, param)
	}
	return out
}

func normalizeOpenAPIMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = normalizeOpenAPIValue(value)
	}
	return out
}

func normalizeOpenAPIValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return normalizeOpenAPIMap(v)
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[fmt.Sprint(key)] = normalizeOpenAPIValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeOpenAPIValue(item)
		}
		return out
	default:
		return v
	}
}

func expandOpenAPIPath(path string, values map[string]any, params []openAPIParameter) (openAPIExpandedPath, error) {
	var err error
	rawPath := openAPITemplateParamPattern.ReplaceAllStringFunc(path, func(match string) string {
		if err != nil {
			return match
		}
		name := strings.TrimSuffix(strings.TrimPrefix(match, "{"), "}")
		value, ok := values[name]
		rendered := openAPIStringValue(value)
		if !ok || rendered == "" {
			err = domain.NewError(domain.CodeValidation, "缺少必填路径参数："+name)
			return match
		}
		return url.PathEscape(rendered)
	})
	if err != nil {
		return openAPIExpandedPath{}, err
	}
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return openAPIExpandedPath{}, domain.NewError(domain.CodeValidation, "构造 OpenAPI 请求路径失败："+err.Error())
	}
	return openAPIExpandedPath{path: decodedPath, rawPath: rawPath}, nil
}

func joinOpenAPIEndpoint(base *url.URL, path openAPIExpandedPath) *url.URL {
	out := *base
	basePath := strings.TrimRight(out.Path, "/")
	baseRawPath := strings.TrimRight(out.EscapedPath(), "/")
	opPath := "/" + strings.TrimLeft(path.path, "/")
	if basePath == "" {
		out.Path = opPath
	} else {
		out.Path = basePath + opPath
	}
	opRawPath := "/" + strings.TrimLeft(path.rawPath, "/")
	if baseRawPath == "" {
		out.RawPath = opRawPath
	} else {
		out.RawPath = baseRawPath + opRawPath
	}
	if out.RawPath == out.Path {
		out.RawPath = ""
	}
	return &out
}

func applyOpenAPIAuth(req *http.Request, params openAPIParams, credential string) {
	value := resolveCredentialPlaceholders(params.authValue, credential)
	if value == "" {
		value = credential
	}
	switch params.authType {
	case "bearer":
		if value != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Bearer "+value)
		}
	case "basic":
		if value != "" && req.Header.Get("Authorization") == "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
		}
	case "api-key-header", "custom-header":
		if params.authName != "" && value != "" {
			req.Header.Set(params.authName, value)
		}
	case "api-key-query":
		if params.authName != "" && value != "" {
			q := req.URL.Query()
			q.Set(params.authName, value)
			req.URL.RawQuery = q.Encode()
		}
	}
}

func marshalOpenAPIResultContent(resp *http.Response, body []byte, truncated bool) json.RawMessage {
	payload := map[string]any{
		"status":     resp.StatusCode,
		"statusText": resp.Status,
		"headers":    resp.Header,
		"truncated":  truncated,
	}
	var decoded any
	if len(body) > 0 && json.Unmarshal(body, &decoded) == nil {
		payload["body"] = decoded
	} else {
		payload["bodyText"] = string(body)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(fmt.Sprintf(`{"status":%d,"bodyText":%q}`, resp.StatusCode, string(body)))
	}
	content, err := json.Marshal([]map[string]string{{
		"type": "text",
		"text": string(data),
	}})
	if err != nil {
		return json.RawMessage(`[{"type":"text","text":"{}"}]`)
	}
	return content
}

func openAPIToolName(operationID, method, path string) string {
	if strings.TrimSpace(operationID) != "" {
		return sanitizeOpenAPIToolName(operationID)
	}
	name := strings.ToLower(method) + "_" + strings.Trim(path, "/")
	return sanitizeOpenAPIToolName(name)
}

func sanitizeOpenAPIToolName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "openapi_tool"
	}
	return out
}

func uniqueOpenAPIToolName(base string, existing map[string]openAPIOperation) string {
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s_%d", base, i)
		if _, ok := existing[name]; !ok {
			return name
		}
	}
}

func isOpenAPIHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete", "head", "options":
		return true
	default:
		return false
	}
}

func isOpenAPIParamLocation(in string) bool {
	switch strings.ToLower(in) {
	case "path", "query", "header", "cookie":
		return true
	default:
		return false
	}
}

func openAPIParamLocationLabel(in string) string {
	switch strings.ToLower(in) {
	case "query":
		return "Query"
	case "header":
		return "Header"
	case "cookie":
		return "Cookie"
	default:
		return "OpenAPI"
	}
}

func openAPIStringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
