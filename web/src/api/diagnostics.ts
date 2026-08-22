import request from '@/api/request'

export async function exportDiagnostics(): Promise<Blob> {
  const res = await request.get<Blob>('/diagnostics/export', { responseType: 'blob' })
  return res.data
}

/**
 * 导出协程泄漏报告（纯文本，含泄漏计数与各泄漏协程的调用栈）。
 *
 * 显式请求 debug=1：该级别才是泄漏子集，级别更高会退化为全量协程转储。
 *
 * 服务端采集需抢占 pprof 包级锁并触发一轮垃圾回收，比常规接口慢得多，故单独放宽超时，
 * 不用全局的 15 秒。同一时刻只允许一次采集，并发触发会被后端以 429 拒绝。
 */
export async function exportGoroutineLeaks(): Promise<Blob> {
  const res = await request.get<Blob>('/diagnostics/goroutine-leaks', {
    params: { debug: 1 },
    responseType: 'blob',
    timeout: 60000,
  })
  return res.data
}
