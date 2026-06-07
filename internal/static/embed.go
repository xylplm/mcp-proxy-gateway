package static

import "embed"

// distFS 内嵌前端构建产物（web/dist 经构建/部署步骤同步至本目录 dist/）。
//
// 设计要点（Req 17.1）：Go 二进制通过 `//go:embed dist/*` 将前端 SPA 产物编译进二进制，
// 由 Static_Server 直接从该 embed.FS 提供，无需独立反向代理（Nginx）。
//
// 本地开发与 CI：Dockerfile 在 `go build` 前执行 `COPY --from=web /web/dist
// ./internal/static/dist`；本地构建/测试时需先运行前端构建并将 web/dist 同步到此处的
// dist/ 目录（仓库内保留最小占位 dist/index.html 以保证 `go build ./...` 始终可编译）。
//
// 注意：go:embed 无法内嵌空目录，故 dist/ 必须至少包含 index.html。
//
//go:embed dist/*
var distFS embed.FS
