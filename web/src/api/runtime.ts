/**
 * 运行环境（本地 stdio 能力探测 + 卷内 npm/pip 共享依赖管理）API。
 *
 * Node / Python / uv 由镜像内置，不存在运行期下载安装；精简镜像（:slim）不含
 * 本地运行时，由 summary.localRuntimeSupported 统一门控相关功能。
 */
import request from '@/api/request'
import type { AxiosRequestConfig } from 'axios'

export interface RuntimeToolStatus {
  name: string
  available: boolean
  path?: string
  warning?: string
}

export interface SandboxCapabilities {
  processHardeningSupported: boolean
  filesystemIsolationSupported?: boolean
  networkIsolationSupported?: boolean
  /** 是否可内核级按主机过滤；bwrap 仅支持 deny 断网，allowlist 仍为策略声明。 */
  hostAllowlistEnforced?: boolean
  isolationBackend?: string
  platform: string
  description: string
}

/** 镜像形态：full 内置 Node/Python/uv，slim 仅网关本体。 */
export type RuntimeImageFlavor = 'full' | 'slim'

export interface RuntimeSummary {
  stdioEnabled: boolean
  commandAllowlist: string[]
  strictCommandAllowlist?: string[]
  strictPackageAllowlist?: string[]
  defaultStdioSecurityMode?: 'standard' | 'strict' | 'unrestricted' | string
  globalFileRoots?: string[]
  strictPathOnlyRuntime?: boolean
  tools: RuntimeToolStatus[]
  availableCount: number
  missingCount: number
  dataDir?: string
  runtimeDir?: string
  pathPrefixes?: string[]
  layoutReady?: boolean
  processHardening?: boolean
  /** 镜像形态，仅供展示与排查；功能门控请用 localRuntimeSupported。 */
  imageFlavor?: RuntimeImageFlavor | string
  /** 本镜像是否提供本地运行时（stdio 上游、npm/pip 依赖管理、脚本中心）。 */
  localRuntimeSupported: boolean
  sandbox?: SandboxCapabilities
  /** 依赖管理（npm/pip）状态。 */
  deps?: RuntimeDepsStatus
  riskNotes: string[]
}

/** 依赖类型：npm / pip。 */
export type RuntimeDepKind = 'npm' | 'pip'

/** 已安装的第三方包。 */
export interface RuntimeDependency {
  name: string
  version: string
  kind: RuntimeDepKind
}

/** 依赖列表结果（对应后端 ListDepsResult）。 */
export interface RuntimeListDepsResult {
  kind: RuntimeDepKind
  ready: boolean
  items: RuntimeDependency[]
  count: number
  warning?: string
  pythonHint?: string
}

/** 依赖安装/卸载结果。 */
export interface RuntimeDepInstallResult {
  kind: RuntimeDepKind
  name: string
  version?: string
  message?: string
}

/** 依赖管理状态（嵌入 Summary.deps；已装包列表通过 listRuntimeDeps 单独获取）。 */
export interface RuntimeDepsStatus {
  depProgress?: {
    kind: RuntimeDepKind
    action: string
    spec?: string
    startedAt: string
  }
  depLogs?: RuntimeDepLogEntry[]
  depError?: string
}

/** 依赖操作日志条目。 */
export interface RuntimeDepLogEntry {
  kind: RuntimeDepKind
  level: 'info' | 'success' | 'error'
  message: string
  at: string
}

export interface DirectoryLaunchEntry {
  id: string
  label?: string
  runtime: string
  command: string
  args: string[]
  cwd?: string
  recommendedMode?: string
}

export interface DirectoryLaunchResult {
  root: string
  manifestPath?: string
  entries: DirectoryLaunchEntry[]
  warnings: string[]
}

export async function inspectRuntimeDirectory(
  path: string,
  fileAccessRoots?: string[],
): Promise<DirectoryLaunchResult> {
  const res = await request.post<DirectoryLaunchResult>('/runtime/directory/inspect', {
    path,
    ...(fileAccessRoots && fileAccessRoots.length > 0 ? { fileAccessRoots } : {}),
  })
  return res.data
}

export async function getRuntimeSummary(): Promise<RuntimeSummary> {
  const res = await request.get<RuntimeSummary>('/runtime/summary')
  return res.data
}

/** 列出某类运行时已安装的第三方包。 */
export async function listRuntimeDeps(kind: RuntimeDepKind): Promise<RuntimeListDepsResult> {
  const res = await request.get<RuntimeListDepsResult>('/runtime/deps', { params: { kind } })
  return res.data
}

/** 安装/升级一个第三方包（可能较久，提高超时）。 */
export async function installRuntimeDep(
  kind: RuntimeDepKind,
  spec: string,
): Promise<RuntimeDepInstallResult> {
  const cfg: AxiosRequestConfig = { timeout: 12 * 60 * 1000 }
  const res = await request.post<RuntimeDepInstallResult>(
    '/runtime/deps/install',
    { kind, spec },
    cfg,
  )
  return res.data
}

/** 卸载一个第三方包。 */
export async function uninstallRuntimeDep(
  kind: RuntimeDepKind,
  name: string,
): Promise<RuntimeDepInstallResult> {
  const cfg: AxiosRequestConfig = { timeout: 6 * 60 * 1000 }
  const res = await request.post<RuntimeDepInstallResult>(
    '/runtime/deps/uninstall',
    { kind, name },
    cfg,
  )
  return res.data
}

export interface RuntimeRequirementsPayload {
  mode: 'auto' | 'manual'
  tools: string[]
  note?: string
}

export interface RuntimeKnownTool {
  name: string
  label: string
  description?: string
  inferFrom?: string[]
  templateRuntimes?: string[]
}

export interface RuntimePreflightItem {
  name: string
  label: string
  required: boolean
  available: boolean
  path?: string
  message?: string
  warning?: string
}

export interface RuntimePreflightAction {
  type: 'open_runtime' | 'open_settings' | string
  label: string
}

export interface RuntimeEffectiveSecurity {
  mode: string
  fileAccess?: { mode?: string; paths?: string[] }
  network?: { mode?: string; hosts?: string[] }
  dependencyPolicy?: string
  allowSelfInstall?: boolean
  processHardening?: boolean
  strictPathOnlyRuntime?: boolean
  commandAllowlist?: string[]
  riskLevel?: string
  note?: string
  requiresAck?: boolean
  policyOnlyIsolation?: boolean
}

export interface RuntimePreflightResult {
  ready: boolean
  transport: string
  command?: string
  requirements: RuntimeRequirementsPayload
  suggestedTools?: string[]
  items: RuntimePreflightItem[]
  stdioEnabled: boolean
  commandAllowed: boolean
  commandError?: string
  runtimeDir?: string
  actions?: RuntimePreflightAction[]
  cached?: boolean
  securityMode?: string
  riskLevel?: string
  securityOk?: boolean
  securityError?: string
  effectiveSecurity?: RuntimeEffectiveSecurity
  fileAccessOk?: boolean
  networkPolicy?: { mode?: string; hosts?: string[] }
}

export async function getRuntimeKnownTools(): Promise<RuntimeKnownTool[]> {
  const res = await request.get<RuntimeKnownTool[]>('/runtime/tools')
  return res.data
}

export async function preflightRuntime(payload: {
  transport: string
  command?: string
  args?: string[]
  cwd?: string
  requirements?: RuntimeRequirementsPayload
  securityProfile?: import('@/api/upstreams').SecurityProfile
  templateRuntimes?: string[]
}): Promise<RuntimePreflightResult> {
  const res = await request.post<RuntimePreflightResult>('/runtime/preflight', payload)
  return res.data
}
