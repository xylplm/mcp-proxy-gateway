import type { RuntimeSummary, RuntimeToolStatus } from '../api/runtime'

export function toolStatusLabel(tool: RuntimeToolStatus): string {
  if (tool.warning) return '权限不足'
  return tool.available ? '可用' : '未检测到'
}

export function toolStatusTone(tool: RuntimeToolStatus): 'success' | 'warning' {
  return tool.available && !tool.warning ? 'success' : 'warning'
}

export function summarizeToolHealth(
  summary: Pick<RuntimeSummary, 'availableCount' | 'missingCount' | 'tools'>,
): string {
  const fromTools = summary.tools?.length ?? 0
  const fromCounts = (summary.availableCount ?? 0) + (summary.missingCount ?? 0)
  const total = Math.max(fromTools, fromCounts)
  if (total <= 0) return '暂无探测项'
  if ((summary.missingCount ?? 0) <= 0) {
    return `已检测到 ${summary.availableCount ?? total} 个常用工具`
  }
  return `可用 ${summary.availableCount ?? 0} · 缺失 ${summary.missingCount ?? 0}`
}

export function stdioPolicyLabel(enabled: boolean): string {
  return enabled ? '本地 stdio 已启用' : '本地 stdio 已禁用'
}

export function formatAllowlist(list: string[] | null | undefined): string {
  if (list == null || list.length === 0) return '未配置（使用服务端默认）'
  return list.join('、')
}

/** 运行时 bin 目录展示（优先 pathPrefixes[0]，否则 runtimeDir/bin）。 */
export function runtimeBinDir(
  summary: Pick<RuntimeSummary, 'runtimeDir' | 'pathPrefixes'>,
): string {
  const prefix = summary.pathPrefixes?.find((p) => p && p.length > 0)
  if (prefix) return prefix
  const root = summary.runtimeDir?.trim()
  if (!root) return ''
  return root.endsWith('/') || root.endsWith('\\') ? `${root}bin` : `${root}/bin`
}

/**
 * 工具缺失时是否展示卷路径引导。
 *
 * 精简镜像没有本地运行时，引导用户往 bin 里放文件只会制造无效操作，
 * 这类环境应改为提示切换完整镜像。
 */
export function shouldShowRuntimeGuide(
  summary: Pick<RuntimeSummary, 'missingCount' | 'runtimeDir'> &
    Partial<Pick<RuntimeSummary, 'localRuntimeSupported'>>,
): boolean {
  return (
    summary.localRuntimeSupported !== false &&
    (summary.missingCount ?? 0) > 0 &&
    !!summary.runtimeDir?.trim()
  )
}

export function runtimeGuideSteps(
  summary: Pick<RuntimeSummary, 'runtimeDir' | 'pathPrefixes'>,
): string[] {
  const bin = runtimeBinDir(summary) || `${summary.runtimeDir ?? ''}/bin`
  return [
    `完整镜像已内置 Node / Python / uv；如需覆盖版本，把可执行文件放入 ${bin}`,
    '放入后需确保有可执行权限（chmod +x），同名文件优先于镜像自带版本',
    '完成后点击「刷新探测」（容器入口 PATH 变更需重启容器）',
  ]
}

export function sandboxHardeningLabel(
  summary: Pick<RuntimeSummary, 'processHardening' | 'sandbox'>,
): string {
  if (summary.processHardening === false) return '进程加固已关闭'
  if (summary.sandbox?.processHardeningSupported) return 'Linux 进程加固已启用'
  return '策略加固已启用'
}
