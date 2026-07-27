/**
 * 脚本中心 API（受管脚本资产，落在网关主机 {dataDir}/scripts/library）。
 */
import request from '@/api/request'

export type ScriptLanguage = 'python' | 'javascript' | string
export type ScriptRiskLevel = 'low' | 'medium' | 'high' | 'critical' | string

export interface ScriptFinding {
  rule: string
  severity: ScriptRiskLevel
  line?: number
  message: string
}

export interface ScriptRiskReport {
  level: ScriptRiskLevel
  score: number
  findings: ScriptFinding[]
}

export interface ScriptItem {
  id: string
  name: string
  description?: string
  language: ScriptLanguage
  runtime: string
  entryFile: string
  tags?: string[]
  status: string
  currentVersion: string
  contentSha256?: string
  risk: ScriptRiskReport
  sizeBytes: number
  createdAt: string
  updatedAt: string
  entryPath?: string
}

export interface ScriptDetail extends ScriptItem {
  content: string
}

export interface ScriptVersion {
  version: string
  contentSha256: string
  sizeBytes: number
  risk: ScriptRiskReport
  note?: string
  createdAt: string
  entryFile: string
}

export interface ScriptDiffResult {
  leftLabel: string
  rightLabel: string
  hunks: string[]
  truncated: boolean
}

export interface ScriptLaunchBinding {
  scriptRef: {
    scriptId: string
    version: string
    entryFile: string
    contentSha256: string
    runtime: string
    entryPath: string
    riskLevel: ScriptRiskLevel
    riskScore: number
  }
  command: string
  args: string[]
  cwd: string
  launchMode: 'script'
}

export async function listScripts(params?: {
  q?: string
  language?: string
  risk?: string
}): Promise<ScriptItem[]> {
  const res = await request.get<{ scripts: ScriptItem[] }>('/scripts', { params })
  return res.data.scripts ?? []
}

export async function getScript(id: string): Promise<ScriptDetail> {
  const res = await request.get<ScriptDetail>(`/scripts/${encodeURIComponent(id)}`)
  return res.data
}

export async function createScript(body: {
  name: string
  description?: string
  language: ScriptLanguage
  runtime?: string
  content: string
  tags?: string[]
  note?: string
}): Promise<ScriptDetail> {
  const res = await request.post<ScriptDetail>('/scripts', body)
  return res.data
}

export async function updateScriptMeta(
  id: string,
  body: { name?: string; description?: string; tags?: string[] },
): Promise<ScriptItem> {
  const res = await request.patch<ScriptItem>(`/scripts/${encodeURIComponent(id)}`, body)
  return res.data
}

export async function saveScriptContent(
  id: string,
  body: { content: string; note?: string },
): Promise<ScriptDetail> {
  const res = await request.put<ScriptDetail>(`/scripts/${encodeURIComponent(id)}/content`, body)
  return res.data
}

export interface DeleteScriptResult {
  deleted?: boolean
  warning?: string
}

export async function deleteScript(id: string): Promise<DeleteScriptResult> {
  const res = await request.delete<DeleteScriptResult>(`/scripts/${encodeURIComponent(id)}`)
  return res.data ?? {}
}

export async function listScriptVersions(id: string): Promise<ScriptVersion[]> {
  const res = await request.get<{ versions: ScriptVersion[] }>(
    `/scripts/${encodeURIComponent(id)}/versions`,
  )
  return res.data.versions ?? []
}

export async function getScriptVersion(
  id: string,
  version: string,
): Promise<{ meta: ScriptVersion; content: string }> {
  const res = await request.get<{ meta: ScriptVersion; content: string }>(
    `/scripts/${encodeURIComponent(id)}/versions/${encodeURIComponent(version)}`,
  )
  return res.data
}

export async function activateScriptVersion(id: string, version: string): Promise<ScriptItem> {
  const res = await request.post<ScriptItem>(
    `/scripts/${encodeURIComponent(id)}/versions/${encodeURIComponent(version)}/activate`,
  )
  return res.data
}

export async function diffScriptVersions(
  id: string,
  left: string,
  right: string,
): Promise<ScriptDiffResult> {
  const res = await request.get<ScriptDiffResult>(`/scripts/${encodeURIComponent(id)}/diff`, {
    params: { left, right },
  })
  return res.data
}

export async function analyzeScriptContent(content: string): Promise<ScriptRiskReport> {
  const res = await request.post<ScriptRiskReport>('/scripts/analyze', { content })
  return res.data
}

export async function buildScriptLaunch(
  id: string,
  version?: string,
): Promise<ScriptLaunchBinding> {
  const res = await request.post<ScriptLaunchBinding>(`/scripts/${encodeURIComponent(id)}/launch`, {
    version: version || undefined,
  })
  return res.data
}
