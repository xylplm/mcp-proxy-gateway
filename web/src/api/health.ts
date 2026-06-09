import request from '@/api/request'

export type HealthStatus = 'ok' | 'degraded' | 'failed' | string

export interface DependencyHealth {
  name: string
  status: HealthStatus
  reason?: string
}

export interface UpstreamHealth {
  id: string
  name: string
  enabled: boolean
  state: string
  lastError?: string
}

export interface XiaoZhiHealth {
  enabled: boolean
  endpoint?: string
  connected: boolean
}

export interface DetailHealthReport {
  status: HealthStatus
  dependencies: DependencyHealth[]
  upstreams: UpstreamHealth[]
  xiaozhi?: XiaoZhiHealth
}

export async function getHealth(): Promise<DetailHealthReport> {
  const res = await request.get<DetailHealthReport>('/health')
  return res.data
}
