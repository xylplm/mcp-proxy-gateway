import type { ToolDetail } from '@/api/tools'

export type ToolRiskLevel = 'high' | 'medium' | 'low'

export interface ToolRiskTag {
  key: string
  label: string
  level: ToolRiskLevel
}

const riskMeta = {
  payment: {
    key: 'payment',
    label: '支付',
    level: 'high',
  },
  delete: {
    key: 'delete',
    label: '删除',
    level: 'high',
  },
  write: {
    key: 'write',
    label: '写入',
    level: 'medium',
  },
  send: {
    key: 'send',
    label: '发送',
    level: 'medium',
  },
} satisfies Record<string, ToolRiskTag>

const readOnlyNamePrefixes = new Set([
  'check',
  'count',
  'describe',
  'fetch',
  'find',
  'get',
  'inspect',
  'list',
  'lookup',
  'query',
  'rank',
  'read',
  'recommend',
  'search',
  'show',
  'status',
  'today',
  'total',
  'view',
])

const actionConnectors = new Set(['and', 'then'])

const paymentNameActions = new Set(['buy', 'charge', 'checkout', 'pay', 'purchase', 'refund'])
const paymentNameObjects = new Set(['bill', 'billing', 'checkout', 'invoice', 'order', 'paid', 'payment'])
const deleteNameActions = new Set(['clear', 'delete', 'destroy', 'drop', 'erase', 'purge', 'remove', 'truncate'])
const writeNameActions = new Set([
  'add',
  'commit',
  'create',
  'deploy',
  'download',
  'edit',
  'execute',
  'export',
  'import',
  'insert',
  'merge',
  'modify',
  'move',
  'patch',
  'receive',
  'refresh',
  'rename',
  'run',
  'save',
  'set',
  'sign',
  'subscribe',
  'sync',
  'update',
  'upload',
  'upsert',
  'write',
])
const sendNameActions = new Set(['email', 'notify', 'post', 'publish', 'send', 'sms', 'webhook'])

const paymentDescriptionActions = [
  zhActionPattern('支付|付款|扣款|退款|购买|付费|结账|充值'),
  enActionPattern('buy|charge|checkout|pay|purchase|refund'),
]
const paymentDescriptionObjects = [/账单|发票|订单|支付记录|付款记录|扣款记录|退款记录/, /\b(bill|billing|invoice|order|paid|payment)\b/i]
const deleteDescriptionActions = [
  zhActionPattern('删除|移除|清空|销毁|擦除'),
  enActionPattern('clear|delete|destroy|drop|erase|purge|remove|truncate'),
]
const writeDescriptionActions = [
  zhActionPattern('添加|新增|创建|更新|修改|编辑|重命名|移动|上传|保存|提交|部署|执行|运行|同步|刷新|订阅|签到|转存|下载|导入|导出|写入'),
  enActionPattern('add|commit|create|deploy|download|edit|execute|export|import|insert|merge|modify|move|patch|refresh|rename|run|save|set|subscribe|sync|update|upload|upsert|write'),
]
const sendDescriptionActions = [
  zhActionPattern('发送|发布|通知|短信|推送'),
  enActionPattern('email|notify|post|publish|send|sms|webhook'),
]

export function toolRiskTags(detail: ToolDetail): ToolRiskTag[] {
  const nameGroups = toolNameTokenGroups(detail)
  const descriptions = toolDescriptions(detail)
  const nameActions = {
    payment: hasNameAction(nameGroups, paymentNameActions),
    delete: hasNameAction(nameGroups, deleteNameActions),
    write: hasNameAction(nameGroups, writeNameActions),
    send: hasNameAction(nameGroups, sendNameActions),
  }
  const descriptionActions = {
    payment: hasDescriptionAction(descriptions, paymentDescriptionActions),
    delete: hasDescriptionAction(descriptions, deleteDescriptionActions),
    write: hasDescriptionAction(descriptions, writeDescriptionActions),
    send: hasDescriptionAction(descriptions, sendDescriptionActions),
  }
  const mutatingNameAction = nameActions.payment || nameActions.delete || nameActions.write || nameActions.send
  const mutatingDescriptionAction =
    descriptionActions.payment || descriptionActions.delete || descriptionActions.write || descriptionActions.send

  const tags: ToolRiskTag[] = []
  if (
    nameActions.payment ||
    descriptionActions.payment ||
    (mutatingNameAction && hasNameToken(nameGroups, paymentNameObjects)) ||
    (mutatingDescriptionAction && hasDescriptionObject(descriptions, paymentDescriptionObjects))
  ) {
    tags.push(riskMeta.payment)
  }
  if (nameActions.delete || descriptionActions.delete) tags.push(riskMeta.delete)
  if (nameActions.write || descriptionActions.write) tags.push(riskMeta.write)
  if (nameActions.send || descriptionActions.send) tags.push(riskMeta.send)
  return mergeCustomRiskTags(tags, detail.policy?.riskTags ?? [])
}

export function highestRiskLevel(tags: ToolRiskTag[]): ToolRiskLevel | null {
  if (tags.some((tag) => tag.level === 'high')) return 'high'
  if (tags.some((tag) => tag.level === 'medium')) return 'medium'
  if (tags.some((tag) => tag.level === 'low')) return 'low'
  return null
}

function toolNameTokenGroups(detail: ToolDetail): string[][] {
  return [
    detail.tool.name,
    detail.tool.originalName,
    ...(detail.sources ?? []).map((source) => source.originalName),
  ]
    .filter(Boolean)
    .map((value) => splitNameTokens(value))
    .filter((tokens) => tokens.length > 0)
}

function toolDescriptions(detail: ToolDetail): string[] {
  const seen = new Set<string>()
  const descriptions = [detail.tool.description, ...(detail.sources ?? []).map((source) => source.description)]
  return descriptions.filter((description) => {
    const value = description?.trim()
    if (!value || seen.has(value)) return false
    seen.add(value)
    return true
  })
}

function splitNameTokens(value: string): string[] {
  return value
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)
}

function hasNameAction(groups: string[][], actions: Set<string>): boolean {
  for (const tokens of groups) {
    for (let i = 0; i < tokens.length; i += 1) {
      if (!actions.has(tokens[i])) continue
      if (i === 0) return true
      if (isReadOnlyName(tokens) && !hasActionConnectorBefore(tokens, i)) continue
      return true
    }
  }
  return false
}

function hasNameToken(groups: string[][], targets: Set<string>): boolean {
  return groups.some((tokens) => tokens.some((token) => targets.has(token)))
}

function isReadOnlyName(tokens: string[]): boolean {
  return tokens.length > 0 && readOnlyNamePrefixes.has(tokens[0])
}

function hasActionConnectorBefore(tokens: string[], actionIndex: number): boolean {
  return tokens.slice(0, actionIndex).some((token) => actionConnectors.has(token))
}

function hasDescriptionAction(descriptions: string[], patterns: RegExp[]): boolean {
  return descriptions.some((description) => patterns.some((pattern) => pattern.test(description)))
}

function hasDescriptionObject(descriptions: string[], patterns: RegExp[]): boolean {
  return descriptions.some((description) => patterns.some((pattern) => pattern.test(description)))
}

function mergeCustomRiskTags(tags: ToolRiskTag[], customTags: string[]): ToolRiskTag[] {
  const out = [...tags]
  const seen = new Set(out.map((tag) => tag.key))
  for (const raw of customTags) {
    const label = raw.trim()
    if (label === '') continue
    const key = `custom:${label}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ key, label, level: 'low' })
  }
  return out
}

function zhActionPattern(words: string): RegExp {
  return new RegExp(`(?:^|[，,；;。]|并且|然后|随后|之后|同时|并|后)\\s*(?:会|将|可|可以|立即|自动|开始|执行)?\\s*(?:${words})`)
}

function enActionPattern(words: string): RegExp {
  return new RegExp(
    `(?:^|[,.;:]|\\band\\b|\\bthen\\b|\\bafter\\b|\\bto\\b)\\s*(?:will\\s+|can\\s+|may\\s+|automatically\\s+|immediately\\s+)?(?:${words})(?=$|[^a-z0-9])`,
    'i',
  )
}
