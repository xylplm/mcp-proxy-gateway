package static

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// 本文件（任务 27.1）实现 Static_Server：由 Go 进程直接提供内嵌 Vue3 SPA 静态资源访问，
// 并对客户端路由路径执行 SPA fallback（Req 17.1、17.2）。
//
// 行为约定：
//   - 命中内嵌文件（如 /、/index.html、/assets/xxx.js、/favicon.ico）时，按文件原样返回，
//     并由 http.FileServer 依据扩展名设置正确的 Content-Type（Req 17.1）。
//   - 请求路径既不属于后端 API 路由（/api/*、/mcp/*、/healthz），又不对应任何内嵌静态
//     文件时，返回入口页面 index.html（HTTP 200），以支持前端 history 模式客户端路由
//     （Req 17.2）。
//   - 落入 API 前缀的路径不由本服务兜底：Static_Server 仅应被挂载在 API 路由之后处理「其余」
//     路径；即便误达本服务，也返回 404 而非 index.html，避免遮蔽 API 语义。

// distDir 为内嵌前端产物在 embed.FS 中的根目录名。
const distDir = "dist"

// indexFile 为前端单页应用入口页面文件名。
const indexFile = "index.html"

// apiRoutePrefixes 列出由后端 API 占用、绝不应被 SPA fallback 兜底的路径前缀（Req 17.2）。
//
// 这些前缀分别对应：管理与对外 API（/api、/mcp）以及公开存活探针（/healthz）。
var apiRoutePrefixes = []string{"/api/", "/mcp/", "/healthz"}

// Server 是 Static_Server 的实现：从内嵌 embed.FS 提供前端静态资源，并对客户端路由路径
// 兜底返回 index.html（Req 17.1、17.2）。
//
// 其零值不可用，应经 New 构造；构造后可作为 http.Handler 挂载，亦可经 GinHandler 适配为
// gin 处理器，由装配层（任务 27.2）置于 API 路由之后兜底「其余」路径。
type Server struct {
	// content 为以 dist/ 为根的内嵌文件子树。
	content fs.FS
	// fileServer 负责按内嵌文件原样响应并设置正确的 Content-Type。
	fileServer http.Handler
}

// New 基于内嵌产物构造 Static_Server（Req 17.1）。
//
// 前端 SPA 产物经构建步骤同步至 dist/ 并通过 `//go:embed dist/*` 编译进二进制，由本服务
// 直接从内嵌 embed.FS 提供，无需独立反向代理。
//
// 返回 error 而非 panic，便于装配层在 dist 缺失（未执行前端构建/同步）时给出清晰诊断。
func New() (*Server, error) {
	return newFromFS(distFS)
}

// newFromFS 由给定的根文件系统（其下含 dist/ 子目录）构造 Server，既是内嵌产物
// （embed.FS 根下含 dist/）的构造路径，也便于单测注入自定义 FS。
//
// 经 fs.Sub 去除 dist/ 前缀后，content 的顶层即应包含 index.html；构造时校验其存在，
// 尽早暴露「dist 未构建/未同步」的装配错误。Server 内部只依赖 fs.FS 抽象内容来源，
// SPA fallback / API 前缀判断 / Content-Type / index.html 200 等逻辑均基于该抽象实现。
func newFromFS(root fs.FS) (*Server, error) {
	content, err := fs.Sub(root, distDir)
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(content, indexFile); err != nil {
		return nil, fmt.Errorf("缺少入口页面 %s：%w", indexFile, err)
	}
	return &Server{
		content:    content,
		fileServer: http.FileServer(http.FS(content)),
	}, nil
}

// isAPIRoute 报告给定请求路径是否属于后端 API 路由，从而不应被 SPA fallback 兜底（Req 17.2）。
//
// 以 "/" 结尾的前缀（/api/、/mcp/）按前缀匹配；不以 "/" 结尾的（/healthz）按精确匹配，
// 以免误伤恰以其为前缀的客户端路由（如 /healthz-page）。
func isAPIRoute(reqPath string) bool {
	for _, prefix := range apiRoutePrefixes {
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(reqPath, prefix) {
				return true
			}
			continue
		}
		if reqPath == prefix {
			return true
		}
	}
	return false
}

// hasFile 报告内嵌静态资源中是否存在与请求路径对应的常规文件（非目录）。
func (s *Server) hasFile(reqPath string) bool {
	name := strings.TrimPrefix(path.Clean("/"+reqPath), "/")
	if name == "" {
		// 根路径对应 index.html，视为存在文件。
		name = indexFile
	}
	info, err := fs.Stat(s.content, name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ServeHTTP 实现 http.Handler：命中内嵌文件则原样返回，否则对非 API 路径兜底 index.html
// （Req 17.1、17.2）。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path

	// API 路由绝不由静态服务兜底；即便误达此处也返回 404，避免遮蔽 API 语义（Req 17.2）。
	if isAPIRoute(reqPath) {
		http.NotFound(w, r)
		return
	}

	// 命中内嵌静态文件：交由 FileServer 原样响应并设置正确的 Content-Type（Req 17.1）。
	if s.hasFile(reqPath) {
		s.fileServer.ServeHTTP(w, r)
		return
	}

	// 其余（客户端路由）路径：兜底返回入口页面以支持前端 history 路由（Req 17.2）。
	s.serveIndex(w, r)
}

// serveIndex 以 HTTP 200 返回入口页面 index.html（Req 17.2）。
//
// 直接读取并写出 index.html 内容，而非借道 http.FileServer——后者会将对 /index.html 的
// 请求 301 重定向到 /，破坏 SPA fallback 的 200 语义。
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := s.content.Open(indexFile)
	if err != nil {
		http.Error(w, "index.html 不可用", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "读取 index.html 失败", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// GinHandler 将 Static_Server 适配为 gin.HandlerFunc，便于装配层（任务 27.2）以
// router.NoRoute(server.GinHandler()) 将其挂载于 API 路由之后，兜底「其余」路径
// （Req 17.1、17.2）。
func (s *Server) GinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.ServeHTTP(c.Writer, c.Request)
	}
}
