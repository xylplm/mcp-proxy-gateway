package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/apikey"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 本文件以内存 fake 注入各窄接口依赖，验证 API Key 管理端点（生命周期、屏蔽规则、
// ACL、限流配置）的路由装配、请求解析与错误映射，无需接真实数据库。

// fakeAPIKeys 是 APIKeyService 的内存实现。
type fakeAPIKeys struct {
	created   apikey.Created
	list      []apikey.Metadata
	got       apikey.Metadata
	getErr    error
	enabled   *bool
	enabledID string
	deleted   string
	createErr error
}

func (f *fakeAPIKeys) Create(_ context.Context, in apikey.CreateInput) (apikey.Created, error) {
	if f.createErr != nil {
		return apikey.Created{}, f.createErr
	}
	f.created = apikey.Created{
		Metadata: apikey.Metadata{ID: "key-new", Name: in.Name, Enabled: true, PlaintextKey: "mpg_secret_plaintext"},
	}
	return f.created, nil
}

func (f *fakeAPIKeys) Get(_ context.Context, id string) (apikey.Metadata, error) {
	if f.getErr != nil {
		return apikey.Metadata{}, f.getErr
	}
	f.got = apikey.Metadata{ID: id, Name: "got"}
	return f.got, nil
}

func (f *fakeAPIKeys) List(_ context.Context) ([]apikey.Metadata, error) {
	return f.list, nil
}

func (f *fakeAPIKeys) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.enabledID = id
	f.enabled = &enabled
	return nil
}

func (f *fakeAPIKeys) Delete(_ context.Context, id string) error {
	f.deleted = id
	return nil
}

// fakeAPIKeyFilters 是 APIKeyFilterService 的内存实现。
type fakeAPIKeyFilters struct {
	created   apikey.Filter
	list      []apikey.Filter
	createErr error
}

func (f *fakeAPIKeyFilters) Create(_ context.Context, in apikey.CreateFilterInput) (apikey.Filter, error) {
	if f.createErr != nil {
		return apikey.Filter{}, f.createErr
	}
	f.created = apikey.Filter{ID: "flt-1", APIKeyID: in.APIKeyID, Pattern: in.Pattern, IsRegex: in.IsRegex, Enabled: in.Enabled}
	return f.created, nil
}

func (f *fakeAPIKeyFilters) List(_ context.Context, apiKeyID string) ([]apikey.Filter, error) {
	return f.list, nil
}

func (f *fakeAPIKeyFilters) SetEnabled(_ context.Context, id string, enabled bool) error { return nil }

func (f *fakeAPIKeyFilters) Delete(_ context.Context, id string) error { return nil }

// fakeACLStore 是 ACLStore 的内存实现。
type fakeACLStore struct {
	created   store.ACLEntry
	list      []store.ACLEntry
	createErr error
}

func (f *fakeACLStore) Create(_ context.Context, entry store.ACLEntry) (store.ACLEntry, error) {
	if f.createErr != nil {
		return store.ACLEntry{}, f.createErr
	}
	f.created = store.ACLEntry{ID: "acl-1", APIKeyID: entry.APIKeyID, CIDR: entry.CIDR}
	return f.created, nil
}

func (f *fakeACLStore) ListByAPIKey(_ context.Context, apiKeyID string) ([]store.ACLEntry, error) {
	return f.list, nil
}

func (f *fakeACLStore) Delete(_ context.Context, id string) error { return nil }

// fakeRateLimitStore 是 RateLimitStore 的内存实现。
type fakeRateLimitStore struct {
	key    store.APIKey
	getErr error
	saved  store.APIKey
}

func (f *fakeRateLimitStore) Get(_ context.Context, id string) (store.APIKey, error) {
	if f.getErr != nil {
		return store.APIKey{}, f.getErr
	}
	f.key.ID = id
	return f.key, nil
}

func (f *fakeRateLimitStore) Update(_ context.Context, key store.APIKey) (store.APIKey, error) {
	f.saved = key
	return key, nil
}

// newTestEngine 构造一个挂载了管理路由（透传中间件）的 gin 引擎。
func newTestEngine(d Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	NewRouter(d).Register(e, func(c *gin.Context) { c.Next() })
	return e
}

// doJSON 发起一个 JSON 请求并返回响应记录器。
func doJSON(e *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// envelopeData 解包统一响应信封 { code, message, data }，返回 data 段的原始 JSON 字节。
//
// 批 1 后所有管理端点响应均为信封结构，测试经此辅助取出 data 再断言，避免每处重复解包。
func envelopeData(t *testing.T, w *httptest.ResponseRecorder) []byte {
	t.Helper()
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析响应信封失败：%v，响应体 %s", err, w.Body.String())
	}
	return env.Data
}

// unmarshalData 解包信封并将其 data 段反序列化到 dst。
func unmarshalData(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	data := envelopeData(t, w)
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("解析响应 data 失败：%v，data 段 %s", err, string(data))
	}
}

// parseErrorEnvelope 解包错误响应信封，返回数字业务码、message 与字段级校验明细（data.fields）。
//
// 失败响应的 data 形如 null 或 {"fields": {...}}；无 fields 时返回空 map，便于调用方直接索引。
func parseErrorEnvelope(t *testing.T, w *httptest.ResponseRecorder) (code int, message string, fields map[string]string) {
	t.Helper()
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Fields map[string]string `json:"fields"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("解析错误响应信封失败：%v，响应体 %s", err, w.Body.String())
	}
	if env.Data.Fields == nil {
		env.Data.Fields = map[string]string{}
	}
	return env.Code, env.Message, env.Data.Fields
}

// TestCreateAPIKeyReturnsPlaintextOnce 验证创建端点返回一次性明文密钥（Req 12.1）。
func TestCreateAPIKeyReturnsPlaintextOnce(t *testing.T) {
	keys := &fakeAPIKeys{}
	e := newTestEngine(Deps{APIKeys: keys})

	w := doJSON(e, http.MethodPost, "/api/admin/apikeys", `{"name":"ci"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("期望 HTTP 201，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	var got apikey.Created
	unmarshalData(t, w, &got)
	if got.PlaintextKey != "mpg_secret_plaintext" {
		t.Errorf("期望返回一次性明文密钥，实际 %q", got.PlaintextKey)
	}
	if got.Name != "ci" {
		t.Errorf("期望名称回填为 ci，实际 %q", got.Name)
	}
}

// TestListAPIKeysWrapsInEnvelope 验证列表端点以 apiKeys 字段包裹返回。
func TestListAPIKeysWrapsInEnvelope(t *testing.T) {
	keys := &fakeAPIKeys{list: []apikey.Metadata{{ID: "k1"}, {ID: "k2"}}}
	e := newTestEngine(Deps{APIKeys: keys})

	w := doJSON(e, http.MethodGet, "/api/admin/apikeys", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var env struct {
		APIKeys []apikey.Metadata `json:"apiKeys"`
	}
	unmarshalData(t, w, &env)
	if len(env.APIKeys) != 2 {
		t.Errorf("期望返回 2 个 API Key，实际 %d", len(env.APIKeys))
	}
}

// TestEnableDisableAPIKey 验证启停端点把状态正确委派给服务。
func TestEnableDisableAPIKey(t *testing.T) {
	keys := &fakeAPIKeys{}
	e := newTestEngine(Deps{APIKeys: keys})

	w := doJSON(e, http.MethodPost, "/api/admin/apikeys/key-7/disable", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if keys.enabledID != "key-7" || keys.enabled == nil || *keys.enabled {
		t.Errorf("期望停用 key-7，实际 id=%q enabled=%v", keys.enabledID, keys.enabled)
	}
}

// TestDeleteAPIKeyReturnsNoContent 验证删除端点成功（信封 data 为 null，HTTP 200）。
func TestDeleteAPIKeyReturnsNoContent(t *testing.T) {
	keys := &fakeAPIKeys{}
	e := newTestEngine(Deps{APIKeys: keys})

	w := doJSON(e, http.MethodDelete, "/api/admin/apikeys/key-3", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	if keys.deleted != "key-3" {
		t.Errorf("期望删除 key-3，实际 %q", keys.deleted)
	}
}

// TestGetAPIKeyNotFoundMapsTo404 验证 NOT_FOUND 错误映射为 HTTP 404。
func TestGetAPIKeyNotFoundMapsTo404(t *testing.T) {
	keys := &fakeAPIKeys{getErr: domain.NewError(domain.CodeNotFound, "API Key 不存在")}
	e := newTestEngine(Deps{APIKeys: keys})

	w := doJSON(e, http.MethodGet, "/api/admin/apikeys/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 HTTP 404，实际 %d", w.Code)
	}
}

// TestServiceUnavailableWhenDependencyNil 验证依赖未接线时返回 503。
func TestServiceUnavailableWhenDependencyNil(t *testing.T) {
	e := newTestEngine(Deps{})

	w := doJSON(e, http.MethodGet, "/api/admin/apikeys", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖未接线期望 HTTP 503，实际 %d", w.Code)
	}
}

// TestCreateAPIKeyFilter 验证 API Key 屏蔽规则创建端点接线正确。
func TestCreateAPIKeyFilter(t *testing.T) {
	filters := &fakeAPIKeyFilters{}
	e := newTestEngine(Deps{APIKeyFilters: filters})

	w := doJSON(e, http.MethodPost, "/api/admin/apikeys/key-1/filters", `{"pattern":"foo*","isRegex":false,"enabled":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("期望 HTTP 201，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if filters.created.APIKeyID != "key-1" || filters.created.Pattern != "foo*" {
		t.Errorf("规则未正确绑定到路径中的 API Key：%+v", filters.created)
	}
}

// TestAPIKeyFilterEnableByRuleID 验证按规则标识启停屏蔽规则。
func TestAPIKeyFilterEnableByRuleID(t *testing.T) {
	filters := &fakeAPIKeyFilters{}
	e := newTestEngine(Deps{APIKeyFilters: filters})

	w := doJSON(e, http.MethodPost, "/api/admin/apikey-filters/flt-9/enable", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
}

func TestAPIKeyFilterChangesInvalidateAggregateCache(t *testing.T) {
	filters := &fakeAPIKeyFilters{}
	agg := &fakeAggregationTools{}
	e := newTestEngine(Deps{APIKeyFilters: filters, Aggregation: agg})

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/admin/apikeys/key-1/filters", `{"pattern":"private*","enabled":true}`},
		{http.MethodPost, "/api/admin/apikey-filters/flt-1/disable", ""},
		{http.MethodDelete, "/api/admin/apikey-filters/flt-1", ""},
	} {
		w := doJSON(e, request.method, request.path, request.body)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("%s %s expected success, got %d: %s", request.method, request.path, w.Code, w.Body.String())
		}
	}
	if agg.invalidations != 3 {
		t.Fatalf("every successful API Key visibility-rule write must invalidate the aggregate cache, got %d", agg.invalidations)
	}
}

// TestCreateACLEntry 验证 ACL 创建端点把路径 API Key 与请求体 CIDR 传给仓储。
func TestCreateACLEntry(t *testing.T) {
	acl := &fakeACLStore{}
	e := newTestEngine(Deps{ACLStore: acl})

	w := doJSON(e, http.MethodPost, "/api/admin/apikeys/key-2/acl", `{"cidr":"10.0.0.0/8"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("期望 HTTP 201，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if acl.created.APIKeyID != "key-2" || acl.created.CIDR != "10.0.0.0/8" {
		t.Errorf("ACL 未正确接线：%+v", acl.created)
	}
}

// TestUpdateRateLimitMergesConfig 验证限流配置更新先读后写、仅覆盖限流字段（Req 21）。
func TestUpdateRateLimitMergesConfig(t *testing.T) {
	rl := &fakeRateLimitStore{key: store.APIKey{Name: "preserve", KeyPrefix: "mpg_abc"}}
	e := newTestEngine(Deps{RateLimitStore: rl})

	w := doJSON(e, http.MethodPut, "/api/admin/apikeys/key-5/ratelimit", `{"rateLimit":100,"rateWindowS":60,"quotaPerDay":1000,"quotaPerMonth":30000}`)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	if rl.saved.RateLimit == nil || *rl.saved.RateLimit != 100 {
		t.Errorf("期望写入 rateLimit=100，实际 %v", rl.saved.RateLimit)
	}
	if rl.saved.RateWindowS == nil || *rl.saved.RateWindowS != 60 {
		t.Errorf("期望写入 rateWindowS=60，实际 %v", rl.saved.RateWindowS)
	}
	if rl.saved.QuotaPerDay == nil || *rl.saved.QuotaPerDay != 1000 {
		t.Errorf("期望写入 quotaPerDay=1000，实际 %v", rl.saved.QuotaPerDay)
	}
	if rl.saved.QuotaPerMonth == nil || *rl.saved.QuotaPerMonth != 30000 {
		t.Errorf("期望写入 quotaPerMonth=30000，实际 %v", rl.saved.QuotaPerMonth)
	}
	// 不可变字段须被保留。
	if rl.saved.Name != "preserve" || rl.saved.KeyPrefix != "mpg_abc" {
		t.Errorf("更新限流时不应改动其它字段：%+v", rl.saved)
	}
}

// TestGetRateLimit 验证读取限流配置仅返回限流字段。
func TestGetRateLimit(t *testing.T) {
	rl := &fakeRateLimitStore{key: store.APIKey{RateLimit: new(50), RateWindowS: new(30), QuotaPerDay: new(1000), QuotaPerMonth: new(30000)}}
	e := newTestEngine(Deps{RateLimitStore: rl})

	w := doJSON(e, http.MethodGet, "/api/admin/apikeys/key-8/ratelimit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际 %d", w.Code)
	}
	var got rateLimitConfigResponse
	unmarshalData(t, w, &got)
	if got.ID != "key-8" || got.RateLimit == nil || *got.RateLimit != 50 {
		t.Errorf("限流配置读取不符：%+v", got)
	}
	if got.QuotaPerDay == nil || *got.QuotaPerDay != 1000 || got.QuotaPerMonth == nil || *got.QuotaPerMonth != 30000 {
		t.Errorf("额度配置读取不符：%+v", got)
	}
}
