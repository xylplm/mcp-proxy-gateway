package template

import "github.com/myGithub/mcp-proxy-gateway/internal/domain"

// builtinTemplates 返回模板市场内置的快捷模板集合（Req 14.1）。
//
// 集合覆盖需求约定的多个分类，每个模板均给出名称、分类、简介、文档链接、传输类型、
// 预设连接参数与占位参数定义（含必填标记与校验规则）。预设连接参数用于表单预填充，
// 占位参数标记需管理员补充的字段；二者共同驱动基于模板的上游创建（任务 20.3）。
//
// 注意：模板中的占位参数仅以 `${name}` 形式在预设参数中引用，真实取值在创建时由
// 管理员填写后注入；此处不内置任何真实凭证。
func builtinTemplates() []Template {
	return []Template{
		{
			ID:        "modelscope-mcp",
			Name:      "ModelScope 魔搭社区",
			Category:  CategoryAIModel,
			Summary:   "接入魔搭社区（ModelScope）托管的远程 MCP 服务，需提供服务地址与访问令牌（API Key）。",
			DocURL:    "https://www.modelscope.cn/docs/mcp",
			Transport: domain.TransportStreamableHTTP,
			PresetParams: map[string]any{
				"headers": map[string]any{
					"Authorization": "Bearer ${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "url",
					Label:    "服务地址",
					Required: true,
					Rule:     ParamRule{Kind: ParamURL, MaxLen: 2048},
				},
				{
					Name:        "apiKey",
					Label:       "访问令牌",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在魔搭社区账户的访问令牌页面获取。",
				},
			},
		},
		{
			ID:        "tavily-search",
			Name:      "Tavily 网页搜索",
			Category:  CategorySearch,
			Summary:   "面向 LLM 的实时网页搜索 MCP 服务，需提供 API Key。",
			DocURL:    "https://docs.tavily.com/",
			Transport: domain.TransportStreamableHTTP,
			PresetParams: map[string]any{
				"url": "https://api.tavily.com/mcp",
				"headers": map[string]any{
					"Authorization": "Bearer ${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "apiKey",
					Label:    "API Key",
					Required: true,
					Rule:     ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
				},
			},
		},
		{
			ID:        "brave-search",
			Name:      "Brave Search 搜索",
			Category:  CategorySearch,
			Summary:   "接入 Brave Search API，提供网页搜索与本地商家检索能力，需提供 Brave Search API Key。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-brave-search",
				},
				"env": map[string]any{
					"BRAVE_API_KEY": "${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "apiKey",
					Label:       "API Key",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Brave Search API 控制台生成。",
				},
			},
		},
		{
			ID:        "firecrawl-mcp",
			Name:      "Firecrawl 网页抓取",
			Category:  CategorySearch,
			Summary:   "接入 Firecrawl MCP，支持网页搜索、抓取、站点地图、批量采集与深度研究，需提供 Firecrawl API Key。",
			DocURL:    "https://github.com/firecrawl/firecrawl-mcp-server",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "firecrawl-mcp",
				},
				"env": map[string]any{
					"FIRECRAWL_API_KEY": "${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "apiKey",
					Label:       "API Key",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Firecrawl 控制台的 API Keys 页面获取。",
				},
			},
		},
		{
			ID:        "exa-search-mcp",
			Name:      "Exa AI 搜索",
			Category:  CategorySearch,
			Summary:   "接入 Exa MCP，提供实时网页搜索、网页抓取、代码搜索与研究检索能力，需提供 Exa API Key。",
			DocURL:    "https://docs.exa.ai/reference/exa-mcp",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "exa-mcp-server",
				},
				"env": map[string]any{
					"EXA_API_KEY": "${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "apiKey",
					Label:       "API Key",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Exa 控制台的 API Keys 页面获取。",
				},
			},
		},
		{
			ID:       "github-mcp",
			Name:     "GitHub 代码托管",
			Category: CategoryDevTools,
			Summary:  "接入 GitHub 官方托管的远程 MCP 服务，支持仓库、Issue 与 PR 操作，需提供个人访问令牌（PAT）。",
			DocURL:   "https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md",
			// 使用 GitHub 官方托管端点而非 docker 本地镜像：网关运行在容器内，
			// 无法执行 docker run（镜像不含 docker CLI，也未挂载宿主 socket）。
			Transport: domain.TransportStreamableHTTP,
			PresetParams: map[string]any{
				"url": "https://api.githubcopilot.com/mcp/",
				"headers": map[string]any{
					"Authorization": "Bearer ${token}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "token",
					Label:       "个人访问令牌",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 GitHub 开发者设置中生成的 Personal Access Token。",
				},
			},
		},
		{
			ID:        "playwright-mcp",
			Name:      "Playwright 浏览器自动化",
			Category:  CategoryDevTools,
			Summary:   "接入 Microsoft Playwright MCP，提供浏览器导航、页面操作、截图与自动化测试能力，无需凭证。",
			DocURL:    "https://github.com/microsoft/playwright-mcp",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@playwright/mcp",
				},
			},
			Placeholders: []Placeholder{},
		},
		{
			ID:        "puppeteer-mcp",
			Name:      "Puppeteer 浏览器自动化",
			Category:  CategoryDevTools,
			Summary:   "接入 Puppeteer MCP，支持网页导航、点击、表单填充、截图与浏览器脚本执行，无需凭证。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/puppeteer",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-puppeteer",
				},
			},
			Placeholders: []Placeholder{},
		},
		{
			ID:        "context7-mcp",
			Name:      "Context7 最新文档",
			Category:  CategoryDevTools,
			Summary:   "接入 Context7 远程 MCP，为代码生成和配置问题提供最新库文档与示例；可为不同账号创建多个上游并配置额度。",
			DocURL:    "https://context7.com/docs/overview",
			Transport: domain.TransportStreamableHTTP,
			PresetParams: map[string]any{
				"url": "https://mcp.context7.com/mcp",
				"headers": map[string]any{
					"CONTEXT7_API_KEY": "${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "apiKey",
					Label:       "API Key",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Context7 控制台生成，用于远程 MCP 鉴权。",
				},
			},
		},
		{
			ID:        "postgres-mcp",
			Name:      "PostgreSQL 数据库",
			Category:  CategoryDatabase,
			Summary:   "通过只读连接访问 PostgreSQL 数据库并提供 schema 检视与查询能力，需提供连接字符串。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/postgres",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-postgres", "${dsn}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "dsn",
					Label:       "连接字符串",
					Required:    true,
					Rule:        ParamRule{Kind: ParamString, MinLen: 1, MaxLen: 2048},
					Description: "形如 postgresql://user:pass@host:5432/dbname 的连接串。",
				},
			},
		},
		{
			ID:        "redis-mcp",
			Name:      "Redis 键值数据库",
			Category:  CategoryDatabase,
			Summary:   "接入 Redis MCP，支持读取、写入、删除和检索 Redis 键值数据，需提供 Redis 连接地址。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/redis",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-redis", "${redisURL}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "redisURL",
					Label:       "Redis 连接地址",
					Required:    true,
					Rule:        ParamRule{Kind: ParamString, MinLen: 1, MaxLen: 2048},
					Description: "形如 redis://localhost:6379 或 redis://:password@host:6379/0。",
				},
			},
		},
		{
			ID:        "filesystem-mcp",
			Name:      "本地文件系统",
			Category:  CategoryFileSystem,
			Summary:   "在受限目录范围内读写本地文件的 MCP 服务，需指定允许访问的根目录。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-filesystem", "${rootDir}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "rootDir",
					Label:    "允许访问的根目录",
					Required: true,
					Rule:     ParamRule{Kind: ParamString, MinLen: 1, MaxLen: 4096},
				},
			},
		},
		{
			ID:        "memory-mcp",
			Name:      "知识图谱记忆",
			Category:  CategoryAIModel,
			Summary:   "接入 Memory MCP，为模型提供本地知识图谱式长期记忆能力，无需凭证。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/memory",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-memory",
				},
			},
			Placeholders: []Placeholder{},
		},
		{
			ID:        "sequential-thinking-mcp",
			Name:      "Sequential Thinking 推理规划",
			Category:  CategoryAIModel,
			Summary:   "接入 Sequential Thinking MCP，为复杂任务提供分步思考、修订与分支推理工具，无需凭证。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-sequential-thinking",
				},
			},
			Placeholders: []Placeholder{},
		},
		{
			ID:        "openai-compatible-mcp",
			Name:      "OpenAI 兼容模型服务",
			Category:  CategoryAIModel,
			Summary:   "接入提供 OpenAI 兼容接口的远程模型 MCP 服务，需提供服务地址与 API Key。",
			DocURL:    "https://platform.openai.com/docs",
			Transport: domain.TransportSSE,
			PresetParams: map[string]any{
				"headers": map[string]any{
					"Authorization": "Bearer ${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "url",
					Label:    "服务地址",
					Required: true,
					Rule:     ParamRule{Kind: ParamURL, MaxLen: 2048},
				},
				{
					Name:     "apiKey",
					Label:    "API Key",
					Required: true,
					Rule:     ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
				},
			},
		},
		{
			ID:        "slack-mcp",
			Name:      "Slack 团队协作",
			Category:  CategoryCollaboration,
			Summary:   "接入 Slack 工作区以收发消息与检索频道，需提供 Bot 令牌与团队 ID。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/slack",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-slack",
				},
				"env": map[string]any{
					"SLACK_BOT_TOKEN": "${botToken}",
					"SLACK_TEAM_ID":   "${teamId}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "botToken",
					Label:    "Bot 令牌",
					Required: true,
					Rule:     ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
				},
				{
					Name:     "teamId",
					Label:    "团队 ID",
					Required: true,
					Rule:     ParamRule{Kind: ParamString, MinLen: 1, MaxLen: 64},
				},
			},
		},
		{
			ID:        "notion-mcp",
			Name:      "Notion 工作空间",
			Category:  CategoryCollaboration,
			Summary:   "接入 Notion 官方 MCP，支持检索、读取和编辑已授权的页面与数据源，需提供 Notion Integration Token。",
			DocURL:    "https://developers.notion.com/docs/mcp",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@notionhq/notion-mcp-server",
				},
				"env": map[string]any{
					"NOTION_TOKEN": "${token}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "token",
					Label:       "Integration Token",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Notion 集成设置中创建内部集成，并把需要访问的页面授权给该集成。",
				},
			},
		},
		{
			ID:        "media-saber-mcp",
			Name:      "Media Saber 媒体订阅",
			Category:  CategoryAutomation,
			Summary:   "接入 Media Saber MCP，通过自然语言订阅电影和电视剧，需提供 Media Saber 服务地址与 API KEY。",
			DocURL:    "https://wiki.msaber.fun/usage/ai/mcp.html",
			Transport: domain.TransportStreamableHTTP,
			PresetParams: map[string]any{
				"headers": map[string]any{
					"Authorization": "Bearer ${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "url",
					Label:       "服务地址",
					Required:    true,
					Rule:        ParamRule{Kind: ParamURL, MaxLen: 2048},
					Description: "填写 Media Saber MCP 地址，通常为 http://IP:端口/message。",
				},
				{
					Name:        "apiKey",
					Label:       "API KEY",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Media Saber 我的信息页面的安全配置中新增或复制 API KEY。",
				},
			},
		},
		{
			ID:           "zapier-mcp",
			Name:         "Zapier 自动化",
			Category:     CategoryAutomation,
			Summary:      "通过 Zapier MCP 触发数千种应用的自动化动作，需提供专属接入地址。",
			DocURL:       "https://zapier.com/mcp",
			Transport:    domain.TransportStreamableHTTP,
			PresetParams: map[string]any{},
			Placeholders: []Placeholder{
				{
					Name:     "url",
					Label:    "接入地址",
					Required: true,
					Rule:     ParamRule{Kind: ParamURL, MaxLen: 2048},
				},
			},
		},
		{
			ID:        "google-maps-mcp",
			Name:      "Google Maps 地图服务",
			Category:  CategoryAutomation,
			Summary:   "接入 Google Maps MCP，支持地理编码、地点检索、路线、距离矩阵与海拔查询，需提供 Google Maps API Key。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/google-maps",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-google-maps",
				},
				"env": map[string]any{
					"GOOGLE_MAPS_API_KEY": "${apiKey}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:        "apiKey",
					Label:       "API Key",
					Required:    true,
					Rule:        ParamRule{Kind: ParamSecret, MinLen: 1, MaxLen: 512},
					Description: "在 Google Cloud 控制台启用 Maps API 后生成。",
				},
			},
		},
		// 以下官方服务只在 PyPI 发布（npm 上没有对应包），必须用 uvx 启动。
		{
			ID:        "fetch-mcp",
			Name:      "网页抓取工具",
			Category:  CategoryOther,
			Summary:   "抓取网页并转换为适合模型读取的内容，无需凭证即可使用。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "uvx",
				"args": []any{
					"mcp-server-fetch",
				},
			},
			Placeholders: []Placeholder{},
		},
		// 未收录 mcp-server-git：它经 GitPython 依赖 git 可执行文件，而完整镜像
		// 基于 python:3.12-slim 不含 git。装上必然启动失败，不放进模板市场。
		{
			ID:        "sqlite-mcp",
			Name:      "SQLite 数据库",
			Category:  CategoryDatabase,
			Summary:   "对本地 SQLite 数据库执行查询与结构探查。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sqlite",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "uvx",
				"args": []any{
					"mcp-server-sqlite", "--db-path", "${dbPath}",
				},
			},
			Placeholders: []Placeholder{
				{
					Name:     "dbPath",
					Label:    "数据库文件路径",
					Required: true,
					Rule:     ParamRule{Kind: ParamString, MinLen: 1, MaxLen: 4096},
				},
			},
		},
	}
}
