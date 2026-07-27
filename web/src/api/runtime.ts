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
  riskNotes: string[]
}

export interface RuntimeInstallResult {
  id: string
  name: string
  version: string
  tools: string[]
  runtimeDir: string
  reused: boolean
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
