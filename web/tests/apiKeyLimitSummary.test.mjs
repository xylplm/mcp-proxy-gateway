import assert from 'node:assert/strict'
import test from 'node:test'
import { apiKeyLimitSummary } from '../src/utils/apiKeyLimitSummary.ts'

test('summarizes disabled API key limits', () => {
  assert.equal(apiKeyLimitSummary({}), '无限制')
  assert.equal(apiKeyLimitSummary({ rateLimit: 100 }), '无限制')
  assert.equal(apiKeyLimitSummary({ rateWindowS: 60 }), '无限制')
})

test('summarizes rate and quota limits together', () => {
  assert.equal(
    apiKeyLimitSummary({
      rateLimit: 100,
      rateWindowS: 60,
      quotaPerDay: 1000,
      quotaPerMonth: 30000,
    }),
    '100/60s · 每日 1000 · 每月 30000',
  )
})

test('ignores non-positive quota values', () => {
  assert.equal(apiKeyLimitSummary({ quotaPerDay: 0, quotaPerMonth: -1 }), '无限制')
  assert.equal(apiKeyLimitSummary({ quotaPerDay: 200 }), '每日 200')
})
