import type { RiskLevel, RiskProfile, RiskStatus } from '@/api/aiRisk'

export const riskLevelLabel: Record<RiskLevel, string> = {
  low: '低风险',
  medium: '中风险',
  high: '高风险',
  blocked: '禁止',
}
export const riskStatusLabel: Record<RiskStatus, string> = {
  pending: '待评级',
  rated: '已评级',
  needs_review: '待复核',
  stale: '已变化',
  error: '失败',
  removed: '已移除',
}
export const riskProfileLabel: Record<RiskProfile, string> = {
  readonly: '只读',
  standard: '标准',
  privileged: '特权',
  legacy_unrestricted: '兼容无限制',
}
export const riskProfileDescription: Record<RiskProfile, string> = {
  readonly: '仅允许低风险工具',
  standard: '允许低风险和中风险工具',
  privileged: '允许低、中、高风险工具',
  legacy_unrestricted: '跳过风险目录，仅用于迁移兼容',
}

export function riskBadgeClass(level: RiskLevel): string {
  return {
    low: 'bg-success-50 text-success-700 dark:bg-success-500/15 dark:text-success-400',
    medium: 'bg-warning-50 text-warning-700 dark:bg-warning-500/15 dark:text-warning-400',
    high: 'bg-error-50 text-error-700 dark:bg-error-500/15 dark:text-error-400',
    blocked: 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900',
  }[level]
}
