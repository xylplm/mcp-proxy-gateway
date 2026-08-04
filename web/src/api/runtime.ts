/**
 * 运行环境（本地 stdio 能力探测 + 受控预置安装）API。
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

export interface RuntimeCatalogPackage {
  id: string
  name: string
  version: string
  description: string
  kind: string
  tools: string[]
  supported: boolean
  installed: boolean
  installedAt?: string
  assetGoos?: string
  assetGoarch?: string
}

export interface RuntimeInstallRecord {
  id: string
  name: string
  version: string
  kind: string
  installedAt: string
  goos: string
  goarch: string
  tools: string[]
}

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
  sandbox?: SandboxCapabilities
  catalog?: RuntimeCatalogPackage[]
  installedPackages?: RuntimeInstallRecord[]
  installProgress?: {
    packageId: string
    phase: string
    bytes: number
    total: number
    startedAt: string
  }
  /** 最近一次安装的结构化日志（成功/失败各阶段），最早在前。 */
  installLogs?: RuntimeInstallLogEntry[]
  /** 最近一次安装失败原因（进度清空后仍保留，便于排查）。 */
  installError?: string
  /** 依赖管理（npm/pip）状态。 */
  deps?: RuntimeDepsStatus
  riskNotes: string[]
}

/** 安装日志条目级别。 */
export type RuntimeInstallLogLevel = 'info' | 'success' | 'error'

/** 运行时安装结构化日志条目（对应后端 InstallLogEntry）。 */
export interface RuntimeInstallLogEntry {
  phase: string
  level: RuntimeInstallLogLevel
  message: string
  source?: string
  bytes?: number
  at: string
}

export interface RuntimeInstallResult {
  id: string
  name: string
  version: string
  tools: string[]
  runtimeDir: string
  reused: boolean
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

export async function getRuntimeCatalog(): Promise<RuntimeCatalogPackage[]> {
  const res = await request.get<RuntimeCatalogPackage[]>('/runtime/catalog')
  return res.data
}

export async function previewRuntimeInstall(packageId: string): Promise<RuntimeCatalogPackage> {
  const res = await request.post<RuntimeCatalogPackage>('/runtime/install/preview', { packageId })
  return res.data
}

/** 安装可能较久，单独提高超时。 */
export async function installRuntimePackage(packageId: string): Promise<RuntimeInstallResult> {
  const cfg: AxiosRequestConfig = { timeout: 12 * 60 * 1000 }
  const res = await request.post<RuntimeInstallResult>('/runtime/install', { packageId }, cfg)
  return res.data
}

export async function uninstallRuntimePackage(
  packageId: string,
): Promise<{ uninstalled: boolean; packageId: string }> {
  const res = await request.post<{ uninstalled: boolean; packageId: string }>(
    '/runtime/uninstall',
    {
      packageId,
    },
  )
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
  packageId?: string
  inferFrom?: string[]
  templateRuntimes?: string[]
}

export interface RuntimePreflightItem {
  name: string
  label: string
  required: boolean
  available: boolean
  path?: string
  fixable: boolean
  packageId?: string
  message?: string
  warning?: string
}

export interface RuntimePreflightAction {
  type: 'install' | 'open_runtime' | 'open_settings' | string
  packageId?: string
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
