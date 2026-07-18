/**
 * 运行环境（本地 stdio 能力探测 + 受控预置安装）API。
 */
import request from '@/api/request'
import type { AxiosRequestConfig } from 'axios'

export interface RuntimeToolStatus {
  name: string
  available: boolean
  path?: string
}

export interface SandboxCapabilities {
  processHardeningSupported: boolean
  platform: string
  description: string
}

export interface RuntimePackageAsset {
  goos: string
  goarch: string
  url: string
  sha256: string
  format: string
}

export interface RuntimeCatalogPackage {
  id: string
  name: string
  version: string
  description: string
  kind: string
  tools: string[]
  assets?: RuntimePackageAsset[]
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

export async function uninstallRuntimePackage(packageId: string): Promise<{ uninstalled: boolean; packageId: string }> {
  const res = await request.post<{ uninstalled: boolean; packageId: string }>('/runtime/uninstall', {
    packageId,
  })
  return res.data
}
