package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGoroutineLeakProfileDefaultsToLeakSubset 验证默认级别返回的是「泄漏子集」而非全量协程。
//
// 这是本端点最容易搞错的一点：runtime/pprof 在 debug >= 2 时会跳过泄漏过滤、退化成等价于
// 普通 goroutine profile 的全量转储。只有 debug <= 1 才带 "goroutineleak profile: total N"
// 头部并仅收录泄漏协程。若默认级别被改错，本用例会失败。
func TestGoroutineLeakProfileDefaultsToLeakSubset(t *testing.T) {
	e := newTestEngine(Deps{})
	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("默认应返回文本格式，实际 Content-Type=%q", ct)
	}
	// 文本格式不应触发浏览器下载。
	if cd := w.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("文本格式不应带下载头，实际 %q", cd)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("应设置 nosniff，避免浏览器对 profile 输出做内容嗅探")
	}

	body := w.Body.String()
	if !strings.Contains(body, "goroutineleak profile") {
		t.Errorf("默认级别应输出泄漏 profile 头部（而非全量协程转储），实际开头为 %q",
			firstLine(body))
	}
}

// TestGoroutineLeakProfileExplicitTextLevel 验证显式 debug=1 与默认级别等价。
func TestGoroutineLeakProfileExplicitTextLevel(t *testing.T) {
	e := newTestEngine(Deps{})
	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks?debug=1", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "goroutineleak profile") {
		t.Errorf("debug=1 应输出泄漏 profile 头部，实际开头为 %q", firstLine(w.Body.String()))
	}
}

// TestGoroutineLeakProfileBinaryIsDownloadable 验证 debug=0 输出 pprof 二进制并作为附件下载，
// 供 go tool pprof 离线分析。
func TestGoroutineLeakProfileBinaryIsDownloadable(t *testing.T) {
	e := newTestEngine(Deps{})
	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks?debug=0", "")

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("debug=0 应返回二进制流，实际 Content-Type=%q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".pprof") {
		t.Errorf("debug=0 应作为 .pprof 附件下载，实际 Content-Disposition=%q", cd)
	}
	if w.Body.Len() == 0 {
		t.Error("pprof 二进制响应体不应为空")
	}
}

// TestGoroutineLeakProfileRejectsUnsupportedDebug 验证不支持的 debug 值以字段级校验错误拒绝。
//
// 特别是 2：它在标准库里合法，但语义是全量协程转储，与本端点名称不符，必须挡在门外而不是
// 静默返回一份名不符实的报告。
func TestGoroutineLeakProfileRejectsUnsupportedDebug(t *testing.T) {
	e := newTestEngine(Deps{})
	for _, bad := range []string{"2", "3", "-1", "abc", "1.5"} {
		w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks?debug="+bad, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("debug=%q 应返回 400，实际 %d：%s", bad, w.Code, w.Body.String())
		}
	}
}

// TestGoroutineLeakProfileRejectsConcurrentCollection 验证并发采集被拒绝而非排队。
//
// 每次采集都要抢 pprof 包级锁并触发一轮垃圾回收，排队会让每个排在后面的请求各自再触发一轮，
// 因此这里要求「同一时刻仅一次，其余立即以 429 拒绝」。
func TestGoroutineLeakProfileRejectsConcurrentCollection(t *testing.T) {
	r := NewRouter(Deps{})
	gin.SetMode(gin.TestMode)
	e := gin.New()
	r.Register(e, func(c *gin.Context) { c.Next() })

	// 抢占闸门，模拟「已有一次采集在进行」。
	if !r.goroutineLeakInFlight.CompareAndSwap(false, true) {
		t.Fatal("初始状态下闸门应可获取")
	}
	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks", "")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("已有采集在进行时应返回 429，实际 %d：%s", w.Code, w.Body.String())
	}
	r.goroutineLeakInFlight.Store(false)

	// 释放后应恢复可用，确认闸门不会粘住。
	if w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks", ""); w.Code != http.StatusOK {
		t.Fatalf("闸门释放后应恢复可用，实际 %d", w.Code)
	}
}

// TestGoroutineLeakProfileReleasesGateAfterConcurrentBurst 验证一轮并发请求过后闸门被正确释放，
// 即 defer 复位没有被错误分支绕过。
func TestGoroutineLeakProfileReleasesGateAfterConcurrentBurst(t *testing.T) {
	r := NewRouter(Deps{})
	gin.SetMode(gin.TestMode)
	e := gin.New()
	r.Register(e, func(c *gin.Context) { c.Next() })

	const parallel = 8
	var wg sync.WaitGroup
	codes := make([]int, parallel)
	for i := range parallel {
		wg.Go(func() {
			codes[i] = doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks", "").Code
		})
	}
	wg.Wait()

	// 并发下允许出现 429（被闸门拒绝），但至少要有一个成功，且不应出现其他状态码。
	success := 0
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			success++
		case http.StatusTooManyRequests:
		default:
			t.Errorf("第 %d 个并发请求返回了意外状态码 %d", i, code)
		}
	}
	if success == 0 {
		t.Error("并发请求中应至少有一次采集成功")
	}

	if r.goroutineLeakInFlight.Load() {
		t.Error("请求全部结束后闸门应已释放")
	}
}

// TestGoroutineLeakProfileNotRegisteredWithoutAdminAuth 是一条安全约束测试：profile 输出包含
// 泄漏协程的调用栈，必须处于管理员鉴权之下。Register 在 adminAuth 为 nil 时不注册任何受保护
// 端点，此处锁定该行为，防止日后重构把诊断端点挪到公开组。
func TestGoroutineLeakProfileNotRegisteredWithoutAdminAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	NewRouter(Deps{}).Register(e, nil)

	w := doJSON(e, http.MethodGet, "/api/admin/diagnostics/goroutine-leaks", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("无鉴权中间件时不应注册该端点，期望 404，实际 %d", w.Code)
	}
}

// firstLine 取文本首行，用于断言失败时给出可读的实际值。
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
