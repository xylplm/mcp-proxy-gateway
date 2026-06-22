export interface UpstreamRateLimits {
  enabled: boolean
  perSecond?: number
  perMinute?: number
  perHour?: number
  perDay?: number
  perWeek?: number
  perMonth?: number
  timezone?: string
}

export function emptyRateLimits(): UpstreamRateLimits {
  return {
    enabled: false,
    perSecond: 0,
    perMinute: 0,
    perHour: 0,
    perDay: 0,
    perWeek: 0,
    perMonth: 0,
    timezone: 'UTC',
  }
}
