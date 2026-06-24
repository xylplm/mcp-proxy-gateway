import type { APIKey, RateLimitConfig } from '@/api/apikeys'

export function apiKeyLimitSummary(config: Pick<APIKey | RateLimitConfig, 'rateLimit' | 'rateWindowS' | 'quotaPerDay' | 'quotaPerMonth'>): string {
  const parts: string[] = []
  if (positive(config.rateLimit) && positive(config.rateWindowS)) {
    parts.push(`${config.rateLimit}/${config.rateWindowS}s`)
  }
  if (positive(config.quotaPerDay)) {
    parts.push(`每日 ${config.quotaPerDay}`)
  }
  if (positive(config.quotaPerMonth)) {
    parts.push(`每月 ${config.quotaPerMonth}`)
  }
  return parts.length > 0 ? parts.join(' · ') : '无限制'
}

function positive(value: number | undefined): value is number {
  return value !== undefined && value > 0
}
