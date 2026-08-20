/**
 * 模板市场（Template_Market）API 封装
 *
 * 设计要点（对应 design.md「模板市场」与 Req 14.1-14.7、14.13）：
 * - 复用全局 Axios 实例（`@/api/request`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 类型与后端 internal/template 包对齐（Template / Category / Placeholder / PrefillForm）。
 *
 * 后端路由（已实现，见 internal/httpapi/templates.go），挂载于管理前缀 `/api/admin/templates` 下：
 *   GET /templates                      列出全部模板
 *   GET /templates/categories           按分类组织的模板视图（分类导航）
 *   GET /templates?category=&keyword=   按分类筛选 / 关键字检索
 *   GET /templates/:id                  模板详情
 *   GET /templates/:id/prefill          基于模板的表单预填充数据
 */
import request from '@/api/request'
import type { ConnParams, TransportType } from '@/api/upstreams'

/** 模板分类标识，与后端 template.Category 对齐。 */
export type TemplateCategory =
  | 'search'
  | 'dev_tools'
  | 'database'
  | 'file_system'
  | 'ai_model'
  | 'collaboration'
  | 'automation'
  | 'other'

/** 占位参数取值类别，与后端 template.ParamKind 对齐。 */
export type ParamKind = 'string' | 'url' | 'secret' | 'int'

export type TemplateTrustLevel = 'curated' | string
export type TemplateRuntime = 'remote' | 'node' | 'python' | 'uvx' | 'local' | string
export type TemplateCredentialType =
  | 'none'
  | 'api_key'
  | 'oauth'
  | 'token'
  | 'connection_string'
  | 'service_url'
  | string
export type TemplateToolType =
  | 'search'
  | 'file'
  | 'database'
  | 'browser'
  | 'project_management'
  | 'collaboration'
  | 'automation'
  | 'ai'
  | 'dev_tools'
  | 'maps'
  | 'other'
  | string

/** 占位参数校验规则，与后端 template.ParamRule 对齐。 */
export interface ParamRule {
  /** 取值类别（决定基础校验方式）。 */
  kind: ParamKind
  /** 可选正则约束（非空时要求完整匹配）。 */
  pattern?: string
  /** 最小长度（按字符计），0 表示不限制。 */
  minLen?: number
  /** 最大长度（按字符计），0 表示不限制。 */
  maxLen?: number
}

/** 模板占位参数定义，与后端 template.Placeholder 对齐（Req 14.1）。 */
export interface Placeholder {
  /** 参数键名（表单字段与连接参数标识）。 */
  name: string
  /** 面向用户的显示名（中文）。 */
  label?: string
  /** 是否必填。 */
  required: boolean
  /** 校验规则。 */
  rule: ParamRule
  /** 补充说明（取值来源/获取方式）。 */
  description?: string
}

/** 模板市场快捷模板，与后端 template.Template 对齐（Req 14.1）。 */
export interface Template {
  /** 模板唯一标识。 */
  id: string
  /** 模板名称（参与检索）。 */
  name: string
  /** 所属分类。 */
  category: TemplateCategory
  /** 简介（参与检索）。 */
  summary: string
  /** 第三方服务文档链接。 */
  docUrl: string
  /** 生成上游时使用的传输类型。 */
  transport: TransportType
  trustLevel?: TemplateTrustLevel
  runtimes?: TemplateRuntime[]
  credentialTypes?: TemplateCredentialType[]
  containerReady?: boolean
  toolTypes?: TemplateToolType[]
  /** 预设连接参数（用于表单预填充，含 ${name} 占位引用）。 */
  presetParams: ConnParams
  /** 占位参数定义集合（标记需用户填写的字段）。 */
  placeholders: Placeholder[]
}

/** 按分类组织的模板视图，与后端 template.CategoryView 对齐（Req 14.2）。 */
export interface CategoryView {
  /** 分类标识。 */
  category: TemplateCategory
  /** 分类中文显示名。 */
  displayName: string
  /** 该分类下的模板列表（可能为空）。 */
  templates: Template[] | null
}

/** 表单预填充数据，与后端 template.PrefillForm 对齐（Req 14.7）。 */
export interface PrefillForm {
  /** 来源模板标识。 */
  templateId: string
  /** 预填充的上游名称（默认取模板名，可修改）。 */
  name: string
  /** 模板固定的传输类型。 */
  transport: TransportType
  /** 预设连接参数（含 ${name} 占位引用）。 */
  presetParams: ConnParams
  /** 需用户填写的占位参数定义。 */
  placeholders: Placeholder[]
}

/** 分类中文显示名映射（前端兜底，后端 categories 接口亦提供 displayName）。 */
export const CATEGORY_LABELS: Record<TemplateCategory, string> = {
  search: '搜索',
  dev_tools: '开发工具',
  database: '数据库与存储',
  file_system: '文件与系统',
  ai_model: 'AI 与模型',
  collaboration: '办公与协作',
  automation: '自动化',
  other: '其他',
}

/** 列表响应体：兼容 { templates: [...] } 与直接数组两种形态。 */
interface ListTemplatesResponse {
  templates?: Template[] | null
}

/** 分类视图响应体：兼容 { categories: [...] } 与直接数组两种形态。 */
interface CategoryViewResponse {
  categories?: CategoryView[] | null
}

/**
 * 列出模板，支持按分类与关键字过滤（Req 14.3、14.4、14.5）。
 * 不存在匹配时返回空数组而非抛错（Req 14.5、14.13）。
 */
export async function listTemplates(params?: {
  category?: TemplateCategory
  keyword?: string
}): Promise<Template[]> {
  const res = await request.get<ListTemplatesResponse | Template[]>('/templates', {
    params: {
      category: params?.category,
      keyword: params?.keyword?.trim() || undefined,
    },
  })
  return normalizeTemplates(res.data)
}

/** 获取按分类组织的模板视图，用于分类导航浏览（Req 14.2）。 */
export async function listTemplateCategories(): Promise<CategoryView[]> {
  const res = await request.get<CategoryViewResponse | CategoryView[]>('/templates/categories')
  const data = res.data
  if (Array.isArray(data)) {
    return data
  }
  return data?.categories ?? []
}

/** 获取模板详情（Req 14.6）。 */
export async function getTemplate(id: string): Promise<Template> {
  const res = await request.get<Template>(`/templates/${encodeURIComponent(id)}`)
  return res.data
}

/** 获取基于模板的表单预填充数据（Req 14.7）。 */
export async function getTemplatePrefill(id: string): Promise<PrefillForm> {
  const res = await request.get<PrefillForm>(`/templates/${encodeURIComponent(id)}/prefill`)
  return res.data
}

/** 归一化模板列表响应（兼容信封与裸数组，null → 空数组）。 */
function normalizeTemplates(data: ListTemplatesResponse | Template[] | null): Template[] {
  if (Array.isArray(data)) {
    return data
  }
  return data?.templates ?? []
}
