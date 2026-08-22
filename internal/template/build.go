package template

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/transport"
)

// 本文件（任务 20.3）实现「基于模板的上游创建」：表单预填充、占位参数校验与上游配置生成。
//
// 流程对应 Req 14.7-14.12：
//   - Prefill：选择模板后返回预填充表单数据，标记待输入的占位参数（Req 14.7）。
//   - BuildUpstream：用管理员填写的占位参数值校验并生成上游配置（Req 14.8-14.12）：
//       * 缺失必填占位参数 → 拒绝并指明参数名（Req 14.10）；
//       * 服务地址（url 类）非法 → 拒绝并保留其他参数（Req 14.9）；
//       * 占位参数其余规则（长度/正则/整数）校验；
//       * 将占位值注入预设参数中的 `${name}` 引用，得到完整连接参数；
//       * 凭证类占位（secret）注入到上游 Credential，同时仍保留在连接参数中以驱动传输；
//       * 最终按需求 2/4 字段校验（名称长度、连接参数齐备且格式合法，Req 14.11）；
//       * 全部通过才返回可持久化的 domain.UpstreamConfig（Req 14.12）。
//
// 校验失败统一返回字段级 VALIDATION 错误（domain.APIError），便于前端定位到具体字段。

// placeholderRefPattern 匹配预设参数中形如 ${name} 的占位引用。
var placeholderRefPattern = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)\}`)

// PrefillForm 为选择模板后返回给前端的表单预填充数据（Req 14.7）。
type PrefillForm struct {
	// TemplateID 为来源模板标识。
	TemplateID string `json:"templateId"`
	// Name 为预填充的上游名称（默认取模板名称，可由用户修改）。
	Name string `json:"name"`
	// Transport 为模板固定的传输类型。
	Transport domain.TransportType `json:"transport"`
	// PresetParams 为预设连接参数（含 `${name}` 占位引用），供表单预填充展示。
	PresetParams map[string]any `json:"presetParams"`
	// Placeholders 为需用户填写的占位参数定义（含必填标记与校验规则）。
	Placeholders []Placeholder `json:"placeholders"`
}

// Prefill 据模板生成表单预填充数据，并把需用户填写的占位参数标记为待输入（Req 14.7）。
//
// 模板不存在时返回 NOT_FOUND 错误。返回的数据均为深拷贝，调用方修改不影响市场内部数据。
func (m *Market) Prefill(templateID string) (PrefillForm, error) {
	t, err := m.Get(templateID)
	if err != nil {
		return PrefillForm{}, err
	}
	return PrefillForm{
		TemplateID:   t.ID,
		Name:         t.Name,
		Transport:    t.Transport,
		PresetParams: t.PresetParams,
		Placeholders: t.Placeholders,
	}, nil
}

// BuildInput 为基于模板创建上游的输入：目标名称、占位参数取值与可选的启停/排序/自动同步。
type BuildInput struct {
	// Name 为上游名称；为空时回退到模板名称。长度按需求 2 校验（1-100）。
	Name string
	// Values 为管理员填写的占位参数取值，键与 Placeholder.Name 对应。
	Values map[string]string
	// Enabled 表示生成的上游是否启用。
	Enabled bool
	// SortOrder 为生成上游的排序顺序。
	SortOrder int
	// AutoSync 表示是否开启自动同步。
	AutoSync bool
}

// BuildUpstream 据模板与管理员填写的占位参数生成可持久化的上游配置（Req 14.8-14.12）。
//
// 校验顺序与失败语义：
//   - 模板不存在 → NOT_FOUND；
//   - 逐个占位参数校验：缺失必填项指明参数名（Req 14.10），url 类非法指明格式错误（Req 14.9），
//     并继续收集其余字段错误，使一次返回尽可能完整的字段级错误（保留其他已填参数，Req 14.9）；
//   - 占位校验通过后，将取值注入预设参数的 `${name}` 引用得到完整连接参数，
//     并把 secret 类占位值额外作为上游 Credential；
//   - 最终按需求 2（名称长度）与需求 4（连接参数齐备且格式合法）校验（Req 14.11）；
//   - 全部通过才返回 domain.UpstreamConfig（Req 14.12）。
//
// 任一校验失败均返回字段级 VALIDATION 错误且不生成配置；占位字段错误键以占位参数名标识，
// 名称错误键为 "name"，连接参数错误沿用 transport 校验的 "connParams.*" 键。
func (m *Market) BuildUpstream(templateID string, in BuildInput) (domain.UpstreamConfig, error) {
	t, err := m.Get(templateID)
	if err != nil {
		return domain.UpstreamConfig{}, err
	}

	// 1) 占位参数校验（缺失必填 / url 非法 / 长度 / 正则 / 整数），收集字段级错误。
	fields := make(map[string]string)
	resolved := make(map[string]string, len(t.Placeholders))
	for _, ph := range t.Placeholders {
		val := strings.TrimSpace(in.Values[ph.Name])
		if val == "" {
			if ph.Required {
				// 缺失必填占位参数：指明参数名（Req 14.10）。
				fields[ph.Name] = fmt.Sprintf("缺少必填参数 %q", placeholderLabel(ph))
			}
			// 非必填且为空：跳过校验与注入。
			continue
		}
		if msg := validatePlaceholderValue(ph, val); msg != "" {
			fields[ph.Name] = msg
			continue
		}
		resolved[ph.Name] = val
	}

	if len(fields) > 0 {
		// 保留其他已填参数（不持久化任何配置），返回字段级错误（Req 14.9、14.10）。
		return domain.UpstreamConfig{}, domain.NewValidationError("基于模板创建上游的参数校验失败", fields)
	}

	// 2) 注入占位取值到预设参数的 `${name}` 引用，得到完整连接参数。
	connParams := injectPlaceholders(t.PresetParams, resolved)

	// 预设中未以 `${name}` 引用的占位参数（如直接作为连接参数键的 url），
	// 按其参数名作为顶层连接参数补入，使其参与后续连接参数校验并生效。
	referenced := referencedNames(t.PresetParams)
	for name, val := range resolved {
		if !referenced[name] {
			connParams[name] = val
		}
	}

	// 3) 组装上游配置：名称回退到模板名；secret 类占位值作为凭证。
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = t.Name
	}
	cfg := domain.UpstreamConfig{
		Name:       name,
		Transport:  t.Transport,
		ConnParams: connParams,
		Credential: secretCredential(t.Placeholders, resolved),
		Enabled:    in.Enabled,
		SortOrder:  in.SortOrder,
		AutoSync:   in.AutoSync,
	}

	// 4) 按需求 2 与需求 4 字段校验（名称长度 + 连接参数齐备且格式合法，Req 14.11）。
	if verr := validateGeneratedConfig(cfg); verr != nil {
		return domain.UpstreamConfig{}, verr
	}

	return cfg, nil
}

// validatePlaceholderValue 按占位参数规则校验取值，返回非空错误说明表示校验失败。
//
// 校验维度：长度（MinLen/MaxLen）、类别（url 合法性、整数）与可选正则完整匹配。
// 服务地址（url 类）非法对应 Req 14.9；其余对应模板占位参数规则（Req 14.1）。
func validatePlaceholderValue(ph Placeholder, val string) string {
	r := ph.Rule
	// 长度校验（按 rune 计）。
	n := len([]rune(val))
	if r.MinLen > 0 && n < r.MinLen {
		return fmt.Sprintf("%s长度不能小于 %d 个字符", placeholderLabel(ph), r.MinLen)
	}
	if r.MaxLen > 0 && n > r.MaxLen {
		return fmt.Sprintf("%s长度不能超过 %d 个字符", placeholderLabel(ph), r.MaxLen)
	}

	switch r.Kind {
	case ParamURL:
		if !isValidURL(val) {
			// 服务地址非法（Req 14.9）。
			return fmt.Sprintf("%s不是合法 URL", placeholderLabel(ph))
		}
	case ParamInt:
		if _, err := strconv.Atoi(val); err != nil {
			return fmt.Sprintf("%s必须为整数", placeholderLabel(ph))
		}
	}

	if r.Pattern != "" {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			// 模板自身正则非法属内置数据问题，按校验失败处理以免放过。
			return fmt.Sprintf("%s的校验规则非法", placeholderLabel(ph))
		}
		if loc := re.FindStringIndex(val); loc == nil || loc[0] != 0 || loc[1] != len(val) {
			return fmt.Sprintf("%s格式不符合要求", placeholderLabel(ph))
		}
	}
	return ""
}

// isValidURL 判定字符串是否为带协议与主机的合法绝对 URL。
func isValidURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

// placeholderLabel 返回占位参数面向用户的标签：优先 Label，否则回退到参数名。
func placeholderLabel(ph Placeholder) string {
	if strings.TrimSpace(ph.Label) != "" {
		return ph.Label
	}
	return ph.Name
}

// secretCredential 从已解析的占位取值中取出首个 secret 类占位值作为上游凭证。
//
// 模板约定凭证类占位（如 API Key / token）以 secret 类标记；若存在多个，取定义顺序首个。
// 无 secret 占位时返回空串（该上游无需凭证）。
func secretCredential(placeholders []Placeholder, resolved map[string]string) string {
	for _, ph := range placeholders {
		if ph.Rule.Kind == ParamSecret {
			if v, ok := resolved[ph.Name]; ok {
				return v
			}
		}
	}
	return ""
}

// validateGeneratedConfig 按需求 2（名称长度 1-100）与需求 4（连接参数）校验生成的上游配置。
//
// 复用 transport.ValidateConnParams 完成传输类型相关的连接参数齐备性与格式校验，
// 并合并名称长度校验，统一返回字段级 VALIDATION 错误（Req 14.11）。
func validateGeneratedConfig(cfg domain.UpstreamConfig) error {
	fields := make(map[string]string)

	if n := len([]rune(cfg.Name)); n < 1 || n > 100 {
		fields["name"] = "名称长度必须在 1 至 100 个字符之间"
	}

	mergeConnParamFields(fields, transport.ValidateConnParams(cfg))

	if len(fields) > 0 {
		return domain.NewValidationError("生成的上游配置字段校验失败", fields)
	}
	return nil
}

// mergeConnParamFields 把 transport.ValidateConnParams 返回的字段级错误并入 fields。
func mergeConnParamFields(fields map[string]string, err error) {
	if err == nil {
		return
	}
	apiErr, ok := err.(*domain.APIError)
	if !ok {
		fields["connParams"] = err.Error()
		return
	}
	maps.Copy(fields, apiErr.Fields)
	if len(apiErr.Fields) == 0 {
		fields["connParams"] = apiErr.Message
	}
}

// injectPlaceholders 深拷贝预设参数并把其中的 `${name}` 引用替换为已解析的占位取值。
//
// 递归处理 map、切片与字符串：
//   - 字符串：替换其中全部 `${name}` 引用；未解析到的引用保持原样（由后续连接参数校验暴露）。
//   - map[string]any / []any：逐元素递归。
//   - 其他类型：原样保留。
//
// 返回的是与模板内部数据无共享的新结构，避免污染内置模板。
func injectPlaceholders(preset map[string]any, values map[string]string) map[string]any {
	out := make(map[string]any, len(preset))
	for k, v := range preset {
		out[k] = injectValue(v, values)
	}
	return out
}

// injectValue 对单个预设值递归注入占位取值。
func injectValue(v any, values map[string]string) any {
	switch tv := v.(type) {
	case string:
		return substituteRefs(tv, values)
	case map[string]any:
		return injectPlaceholders(tv, values)
	case []any:
		out := make([]any, len(tv))
		for i, item := range tv {
			out[i] = injectValue(item, values)
		}
		return out
	case []string:
		out := make([]string, len(tv))
		for i, item := range tv {
			out[i] = substituteRefs(item, values)
		}
		return out
	default:
		return v
	}
}

// substituteRefs 将字符串中的 `${name}` 引用替换为对应占位取值；未知引用保持原样。
func substituteRefs(s string, values map[string]string) string {
	return placeholderRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1] // 去掉 ${ 与 }
		if val, ok := values[name]; ok {
			return val
		}
		return match
	})
}

// referencedNames 收集预设参数中通过 `${name}` 引用到的全部占位参数名。
//
// 用于区分「以引用方式嵌入预设结构的占位」与「需作为顶层连接参数补入的占位」（如 url）。
func referencedNames(preset map[string]any) map[string]bool {
	names := make(map[string]bool)
	collectRefs(preset, names)
	return names
}

// collectRefs 递归遍历预设值，收集其中 `${name}` 引用的名称。
func collectRefs(v any, names map[string]bool) {
	switch tv := v.(type) {
	case string:
		for _, sm := range placeholderRefPattern.FindAllStringSubmatch(tv, -1) {
			names[sm[1]] = true
		}
	case map[string]any:
		for _, item := range tv {
			collectRefs(item, names)
		}
	case []any:
		for _, item := range tv {
			collectRefs(item, names)
		}
	case []string:
		for _, item := range tv {
			collectRefs(item, names)
		}
	}
}
