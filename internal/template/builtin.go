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
			ID:        "github-mcp",
			Name:      "GitHub 代码托管",
			Category:  CategoryDevTools,
			Summary:   "接入 GitHub 官方 MCP 服务，支持仓库、Issue 与 PR 操作，需提供个人访问令牌（PAT）。",
			DocURL:    "https://github.com/github/github-mcp-server",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "docker",
				"args": []any{
					"run", "-i", "--rm",
					"-e", "GITHUB_PERSONAL_ACCESS_TOKEN",
					"ghcr.io/github/github-mcp-server",
				},
				"env": map[string]any{
					"GITHUB_PERSONAL_ACCESS_TOKEN": "${token}",
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
			ID:        "fetch-mcp",
			Name:      "网页抓取工具",
			Category:  CategoryOther,
			Summary:   "抓取网页并转换为适合模型读取的内容，无需凭证即可使用。",
			DocURL:    "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
			Transport: domain.TransportStdio,
			PresetParams: map[string]any{
				"command": "npx",
				"args": []any{
					"-y", "@modelcontextprotocol/server-fetch",
				},
			},
			Placeholders: []Placeholder{},
		},
	}
}
