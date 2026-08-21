/**
 * 运行时能力（是否提供本地运行时）共享读取。
 *
 * 完整镜像（:latest / :full）内置 Node / Python / uv，支持 stdio 上游、npm/pip 依赖
 * 与脚本中心；精简镜像（:slim）不含任何本地运行时。多个入口（侧边栏、模板市场、
 * 上游表单）都要按此门控，故在模块级缓存一次探测结果，避免每个组件各拉一次。
 *
 * 只暴露门控所需的布尔值。镜像形态字符串仅用于运行环境页展示，那里已有完整
 * summary，不需要经由本模块。
 */
import type { Ref } from 'vue'
import { ref } from 'vue'
import { getRuntimeSummary } from '@/api/runtime'

/**
 * 乐观默认为 true：探测失败或尚未返回时不隐藏功能。
 * 藏错了用户找不到入口，多显示一个入口最多是操作时报错，代价更小。
 */
const localRuntimeSupported = ref(true)
let inflight: Promise<void> | null = null

async function fetchCapability(): Promise<void> {
  try {
    const summary = await getRuntimeSummary()
    localRuntimeSupported.value = summary.localRuntimeSupported !== false
  } catch {
    // 失败时保持乐观默认，并允许下次调用重试（例如登录前的首个请求 401）。
    inflight = null
  }
}

/** 触发一次探测（幂等），返回能力状态。 */
export function useRuntimeCapability(): { localRuntimeSupported: Ref<boolean> } {
  if (inflight === null) inflight = fetchCapability()
  return { localRuntimeSupported }
}

/** 重置缓存：退出登录后应重新探测，避免沿用上一次部署的门控结果。 */
export function resetRuntimeCapability(): void {
  inflight = null
  localRuntimeSupported.value = true
}
