package template

import "github.com/myGithub/mcp-proxy-gateway/internal/domain"

// Category 表示快捷模板所属的分类维度（Req 14.2）。
//
// 取值采用稳定的英文标识，便于作为 API 查询参数与前端筛选键；其面向用户的中文
// 显示名通过 CategoryDisplayName 获取。
type Category string

const (
	// CategorySearch 表示「搜索」分类（如网页/学术搜索类 MCP 服务）。
	CategorySearch Category = "search"
	// CategoryDevTools 表示「开发工具」分类（如代码托管、CI、调试类服务）。
	CategoryDevTools Category = "dev_tools"
	// CategoryDatabase 表示「数据库与存储」分类（如关系型/对象存储类服务）。
	CategoryDatabase Category = "database"
	// CategoryFileSystem 表示「文件与系统」分类（如本地文件、Shell 类服务）。
	CategoryFileSystem Category = "file_system"
	// CategoryAIModel 表示「AI 与模型」分类（如模型推理、向量检索类服务）。
	CategoryAIModel Category = "ai_model"
	// CategoryCollaboration 表示「办公与协作」分类（如即时通讯、文档协作类服务）。
	CategoryCollaboration Category = "collaboration"
	// CategoryAutomation 表示「自动化」分类（如工作流编排、定时任务类服务）。
	CategoryAutomation Category = "automation"
	// CategoryOther 表示「其他」分类，承载未归入上述维度的模板。
	CategoryOther Category = "other"
)

// orderedCategories 为分类的稳定展示顺序，至少包含需求约定的 8 个分类（Req 14.2）。
var orderedCategories = []Category{
	CategorySearch,
	CategoryDevTools,
	CategoryDatabase,
	CategoryFileSystem,
	CategoryAIModel,
	CategoryCollaboration,
	CategoryAutomation,
	CategoryOther,
}

// categoryDisplayNames 为各分类的中文显示名。
var categoryDisplayNames = map[Category]string{
	CategorySearch:        "搜索",
	CategoryDevTools:      "开发工具",
	CategoryDatabase:      "数据库与存储",
	CategoryFileSystem:    "文件与系统",
	CategoryAIModel:       "AI 与模型",
	CategoryCollaboration: "办公与协作",
	CategoryAutomation:    "自动化",
	CategoryOther:         "其他",
}

// Categories 返回模板市场支持的全部分类，按稳定的展示顺序排列（Req 14.2）。
//
// 返回的切片为独立副本，调用方对其修改不影响内部顺序定义。
func Categories() []Category {
	out := make([]Category, len(orderedCategories))
	copy(out, orderedCategories)
	return out
}

// CategoryDisplayName 返回分类的中文显示名；未知分类回退为其原始标识。
func CategoryDisplayName(c Category) string {
	if name, ok := categoryDisplayNames[c]; ok {
		return name
	}
	return string(c)
}

// ParamKind 表示占位参数的取值类别，决定其校验方式（Req 14.1）。
type ParamKind string

const (
	// ParamString 表示普通字符串参数。
	ParamString ParamKind = "string"
	// ParamURL 表示需为合法 URL 的参数（如服务地址）。
	ParamURL ParamKind = "url"
	// ParamSecret 表示敏感凭证参数（如 API Key），前端应以密码框承载且不回显。
	ParamSecret ParamKind = "secret"
	// ParamInt 表示需为整数的参数。
	ParamInt ParamKind = "int"
)

// ParamRule 描述单个占位参数的校验规则（Req 14.1）。
//
// 该结构为纯数据，承载校验意图；具体的校验执行在基于模板的上游创建流程（任务 20.3）
// 中依据这些规则进行。零值表示对应维度不作约束。
type ParamRule struct {
	// Kind 为参数取值类别，决定基础校验方式（字符串/URL/凭证/整数）。
	Kind ParamKind `json:"kind"`
	// Pattern 为可选的正则约束，非空时要求参数值完整匹配该模式。
	Pattern string `json:"pattern,omitempty"`
	// MinLen 为参数值的最小长度（按字符计），0 表示不限制。
	MinLen int `json:"minLen,omitempty"`
	// MaxLen 为参数值的最大长度（按字符计），0 表示不限制。
	MaxLen int `json:"maxLen,omitempty"`
}

// TrustLevel 表示模板来源可信度提示，仅用于管理台展示与筛选辅助。
type TrustLevel string

const (
	TrustCurated TrustLevel = "curated"
)

// RuntimeTag 表示运行模板所需或推荐的运行环境。
type RuntimeTag string

const (
	RuntimeRemote RuntimeTag = "remote"
	RuntimeDocker RuntimeTag = "docker"
	RuntimeNode   RuntimeTag = "node"
	RuntimePython RuntimeTag = "python"
	RuntimeUVX    RuntimeTag = "uvx"
	RuntimeLocal  RuntimeTag = "local"
)

// CredentialType 表示接入模板时需要的凭证或配置类型。
type CredentialType string

const (
	CredentialNone             CredentialType = "none"
	CredentialAPIKey           CredentialType = "api_key"
	CredentialOAuth            CredentialType = "oauth"
	CredentialToken            CredentialType = "token"
	CredentialConnectionString CredentialType = "connection_string"
	CredentialServiceURL       CredentialType = "service_url"
)

// ToolType 表示模板大致提供的工具能力类型。
type ToolType string

const (
	ToolTypeSearch            ToolType = "search"
	ToolTypeFile              ToolType = "file"
	ToolTypeDatabase          ToolType = "database"
	ToolTypeBrowser           ToolType = "browser"
	ToolTypeProjectManagement ToolType = "project_management"
	ToolTypeCollaboration     ToolType = "collaboration"
	ToolTypeAutomation        ToolType = "automation"
	ToolTypeAI                ToolType = "ai"
	ToolTypeDevTools          ToolType = "dev_tools"
	ToolTypeMaps              ToolType = "maps"
	ToolTypeOther             ToolType = "other"
)

// Placeholder 表示模板中需由管理员填写的占位参数定义（Req 14.1）。
//
// 占位参数区别于预设连接参数：后者由模板预先固定并用于表单预填充，前者必须由管理员
// 输入（如服务地址、API Key）。Name 为参数键，与生成上游配置时写入连接参数的键一致。
type Placeholder struct {
	// Name 为占位参数键名，是表单字段与连接参数的标识。
	Name string `json:"name"`
	// Label 为面向用户的显示名（中文），便于表单展示。
	Label string `json:"label,omitempty"`
	// Required 表示该占位参数是否必填。
	Required bool `json:"required"`
	// Rule 为该占位参数的校验规则。
	Rule ParamRule `json:"rule"`
	// Description 为补充说明（如取值来源、获取方式），可为空。
	Description string `json:"description,omitempty"`
}

// Template 表示模板市场中的一个内置快捷模板（Req 14.1）。
//
// 每个模板携带浏览/检索所需的元信息（名称、分类、简介、文档链接）与接入所需的接入信息
// （传输类型、预设连接参数、占位参数定义）。预设连接参数用于表单预填充，占位参数标记
// 需用户补充的字段，二者共同驱动基于模板的上游创建（任务 20.3）。
type Template struct {
	// ID 为模板唯一标识，用于详情查询与基于模板创建。
	ID string `json:"id"`
	// Name 为模板名称（面向用户展示，参与关键字检索）。
	Name string `json:"name"`
	// Category 为模板所属分类。
	Category Category `json:"category"`
	// Summary 为模板简介（参与关键字检索）。
	Summary string `json:"summary"`
	// DocURL 为模板对应第三方服务的文档链接。
	DocURL string `json:"docUrl"`
	// Transport 为该模板生成上游时使用的传输类型。
	Transport domain.TransportType `json:"transport"`
	// TrustLevel 为内置模板来源可信度提示。
	TrustLevel TrustLevel `json:"trustLevel"`
	// Runtimes 为运行该模板依赖的环境标签。
	Runtimes []RuntimeTag `json:"runtimes"`
	// CredentialTypes 为接入该模板需要的凭证或配置类型。
	CredentialTypes []CredentialType `json:"credentialTypes"`
	// ContainerReady 表示该模板是否天然适合在容器/远程环境内运行。
	ContainerReady bool `json:"containerReady"`
	// ToolTypes 为模板提供的工具能力类型。
	ToolTypes []ToolType `json:"toolTypes"`
	// PresetParams 为预设连接参数，用于表单预填充；不含需用户填写的占位参数。
	PresetParams map[string]any `json:"presetParams"`
	// Placeholders 为占位参数定义集合，标记需用户填写的字段及其校验规则。
	Placeholders []Placeholder `json:"placeholders"`
}

// clone 返回模板的深拷贝，避免调用方修改返回值影响内部内置数据。
func (t Template) clone() Template {
	cp := t
	if t.PresetParams != nil {
		params := make(map[string]any, len(t.PresetParams))
		for k, v := range t.PresetParams {
			params[k] = v
		}
		cp.PresetParams = params
	}
	if t.Placeholders != nil {
		phs := make([]Placeholder, len(t.Placeholders))
		copy(phs, t.Placeholders)
		cp.Placeholders = phs
	}
	if t.Runtimes != nil {
		cp.Runtimes = append([]RuntimeTag(nil), t.Runtimes...)
	}
	if t.CredentialTypes != nil {
		cp.CredentialTypes = append([]CredentialType(nil), t.CredentialTypes...)
	}
	if t.ToolTypes != nil {
		cp.ToolTypes = append([]ToolType(nil), t.ToolTypes...)
	}
	return cp
}
