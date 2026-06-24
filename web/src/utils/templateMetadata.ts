import type {
  Template,
  TemplateCredentialType,
  TemplateRuntime,
  TemplateToolType,
  TemplateTrustLevel,
} from '@/api/templates'

const trustLabels: Record<string, string> = {
  curated: '内置精选',
}

const runtimeLabels: Record<string, string> = {
  remote: '远程',
  docker: 'Docker',
  node: 'Node',
  python: 'Python',
  uvx: 'uvx',
  local: '本地',
}

const credentialLabels: Record<string, string> = {
  none: '无需凭证',
  api_key: 'API Key',
  oauth: 'OAuth',
  token: 'Token',
  connection_string: '连接串',
  service_url: '服务地址',
}

const toolTypeLabels: Record<string, string> = {
  search: '搜索',
  file: '文件',
  database: '数据库',
  browser: '浏览器',
  project_management: '项目管理',
  collaboration: '协作',
  automation: '自动化',
  ai: 'AI',
  dev_tools: '开发工具',
  maps: '地图',
  other: '其他',
}

export interface TemplateMetaChip {
  key: string
  label: string
  tone: 'brand' | 'gray' | 'success' | 'warning'
}

export function trustLevelLabel(value?: TemplateTrustLevel): string {
  return labelFromMap(value, trustLabels)
}

export function runtimeLabel(value?: TemplateRuntime): string {
  return labelFromMap(value, runtimeLabels)
}

export function credentialTypeLabel(value?: TemplateCredentialType): string {
  return labelFromMap(value, credentialLabels)
}

export function toolTypeLabel(value?: TemplateToolType): string {
  return labelFromMap(value, toolTypeLabels)
}

export function templateMetaChips(template: Template): TemplateMetaChip[] {
  const credentials = normalizeList(template.credentialTypes)
  const runtimes = normalizeList(template.runtimes)
  const toolTypes = normalizeList(template.toolTypes)
  return [
    {
      key: `trust:${template.trustLevel || 'curated'}`,
      label: trustLevelLabel(template.trustLevel || 'curated'),
      tone: 'success',
    },
    ...credentials.map((item) => ({
      key: `credential:${item}`,
      label: credentialTypeLabel(item),
      tone: item === 'none' ? 'gray' : 'warning',
    }) satisfies TemplateMetaChip),
    ...runtimes.map((item) => ({
      key: `runtime:${item}`,
      label: runtimeLabel(item),
      tone: item === 'docker' || item === 'remote' ? 'brand' : 'gray',
    }) satisfies TemplateMetaChip),
    {
      key: `container:${template.containerReady ? 'ready' : 'local'}`,
      label: template.containerReady ? '容器友好' : '偏本地运行',
      tone: template.containerReady ? 'brand' : 'gray',
    },
    ...toolTypes.map((item) => ({
      key: `type:${item}`,
      label: toolTypeLabel(item),
      tone: 'gray',
    }) satisfies TemplateMetaChip),
  ]
}

export function templateCardChips(template: Template): TemplateMetaChip[] {
  const chips = templateMetaChips(template)
  const credential = chips.find((chip) => chip.key.startsWith('credential:'))
  const runtime = chips.find((chip) => chip.key.startsWith('runtime:'))
  const toolType = chips.find((chip) => chip.key.startsWith('type:'))
  return [credential, runtime, toolType].filter((chip): chip is TemplateMetaChip => chip !== undefined)
}

export function templateMetadataSearchText(template: Template): string {
  return templateMetaChips(template).map((chip) => chip.label).join(' ')
}

function normalizeList<T extends string>(items?: T[]): T[] {
  if (!Array.isArray(items)) return []
  const seen = new Set<T>()
  const out: T[] = []
  for (const raw of items) {
    const item = raw.trim() as T
    if (item === '' || seen.has(item)) continue
    seen.add(item)
    out.push(item)
  }
  return out
}

function labelFromMap(value: string | undefined, labels: Record<string, string>): string {
  if (!value) return ''
  return labels[value] ?? value
}
