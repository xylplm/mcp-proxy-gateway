<script setup lang="ts">
import type { AssessmentJob } from '@/api/aiRisk'

defineProps<{ job: AssessmentJob }>()
defineEmits<{ cancel: [job: AssessmentJob] }>()

function progress(job: AssessmentJob): number {
  return job.requestedCount > 0
    ? Math.min(100, Math.round((job.processedCount / job.requestedCount) * 100))
    : 0
}
const jobStatusLabel: Record<AssessmentJob['status'], string> = {
  queued: '等待中',
  running: '评级中',
  completed: '已完成',
  partial: '部分完成',
  failed: '失败',
  cancelled: '已取消',
}
const jobErrorLabel: Record<string, string> = {
  http_429: 'Provider 限流（HTTP 429）',
  http_503: 'Provider 暂不可用（HTTP 503）',
  http_502: 'Provider 网关错误（HTTP 502）',
  http_504: 'Provider 响应超时（HTTP 504）',
  network: '网络请求失败',
  response_validation: '响应格式不符合要求',
  storage: '评级结果保存失败',
}
function errorEntries(job: AssessmentJob): [string, number][] {
  return Object.entries(job.errorCounts ?? {})
    .filter(([, count]) => count > 0)
    .sort((a, b) => b[1] - a[1])
}
function errorLabel(category: string): string {
  if (jobErrorLabel[category]) return jobErrorLabel[category]
  const status = category.match(/^http_(\d{3})$/)?.[1]
  return status ? 'Provider 返回 HTTP ' + status : category
}
function jobAdvice(job: AssessmentJob): string {
  const categories = new Set(errorEntries(job).map(([category]) => category))
  if (!categories.size) {
    const status = job.lastError.match(/HTTP\s+(\d{3})/)?.[1]
    if (status) categories.add('http_' + status)
  }
  if (categories.has('http_429'))
    return 'Provider 触发了限流。系统会按 Retry-After 退避；仍有失败时，可降低并发或稍后重试。'
  if ([...categories].some((category) => /^http_5\d\d$/.test(category)))
    return 'Provider 服务暂时不可用。新任务会自动重试并尝试拆分批次；可稍后重新评级失败项。'
  if (categories.has('response_validation'))
    return 'Provider 返回内容无法通过结构校验，请检查模型是否支持结构化 JSON 输出。'
  if (categories.has('network')) return '请检查本机到 Provider 的网络、域名解析和超时配置。'
  return ''
}
function jobSetting(job: AssessmentJob, key: string): number | undefined {
  const value = job.scopePayload?.[key]
  return typeof value === 'number' ? value : undefined
}
function statusClass(status: string): string {
  return status === 'completed'
    ? 'text-success-600 dark:text-success-400'
    : status === 'failed'
      ? 'text-error-600 dark:text-error-400'
      : status === 'partial'
        ? 'text-warning-600 dark:text-warning-400'
        : status === 'running'
          ? 'text-brand-600 dark:text-brand-400'
          : 'text-gray-500 dark:text-gray-400'
}
function progressClass(status: AssessmentJob['status']): string {
  if (status === 'failed') return 'bg-error-500'
  if (status === 'partial') return 'bg-warning-500'
  if (status === 'completed') return 'bg-success-500'
  return 'bg-brand-500'
}
function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="text-gray-800 dark:text-gray-200">
    <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
      <span :class="statusClass(job.status)">
        {{ job.scope === 'needs_review' ? '待复核重评' : '常规评级' }} ·
        {{ jobStatusLabel[job.status] }} · 已处理 {{ job.processedCount }}/{{ job.requestedCount }}
      </span>
      <div class="flex items-center gap-3">
        <span class="text-xs text-gray-400">{{ formatTime(job.createdAt) }}</span>
        <button
          v-if="job.status === 'queued' || job.status === 'running'"
          class="text-error-600"
          @click="$emit('cancel', job)"
        >
          取消
        </button>
      </div>
    </div>
    <div class="mt-2 h-1.5 overflow-hidden rounded bg-gray-100 dark:bg-gray-800">
      <div
        class="h-full"
        :class="progressClass(job.status)"
        :style="{ width: progress(job) + '%' }"
      ></div>
    </div>
    <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-600 dark:text-gray-300">
      <span>评级成功 {{ job.successCount }}</span>
      <span v-if="job.reviewCount">其中待复核 {{ job.reviewCount }}</span>
      <span :class="job.failureCount ? 'text-error-600' : ''">失败 {{ job.failureCount }}</span>
      <span v-if="job.retryCount">批次重试 {{ job.retryCount }} 次</span>
      <span v-if="job.splitCount">拆分 {{ job.splitCount }} 次</span>
      <span v-if="jobSetting(job, 'batchSize')">批大小 {{ jobSetting(job, 'batchSize') }}</span>
      <span v-if="jobSetting(job, 'maxConcurrency')"
        >并发 {{ jobSetting(job, 'maxConcurrency') }}</span
      >
    </div>
    <div
      v-if="errorEntries(job).length"
      class="border-error-200 dark:border-error-800 mt-2 space-y-1 border-l-2 pl-3 text-xs"
    >
      <p
        v-for="[category, count] in errorEntries(job)"
        :key="category"
        class="text-error-600 dark:text-error-400"
      >
        {{ errorLabel(category) }}：影响 {{ count }} 个工具
      </p>
    </div>
    <p
      v-else-if="job.failureCount > 0"
      class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400"
    >
      该任务创建于错误分类统计启用之前，只保留了最后一次错误；新任务会分别统计
      429、503、网络及响应格式错误。
    </p>
    <p v-if="jobAdvice(job)" class="mt-2 text-xs leading-5 text-gray-600 dark:text-gray-400">
      {{ jobAdvice(job) }}
    </p>
    <p
      v-if="job.lastError"
      class="mt-1 text-xs leading-5 break-words text-gray-500 dark:text-gray-400"
    >
      最后一次错误：{{ job.lastError }}
    </p>
  </div>
</template>
