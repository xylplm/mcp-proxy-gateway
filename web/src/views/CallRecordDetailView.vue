<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { getCallRecord, type CallRecord } from '@/api/stats'

const route = useRoute()
const router = useRouter()

const record = ref<CallRecord | null>(null)
const loading = ref(false)
const errorMessage = ref('')

const detailId = computed(() => String(route.params.id ?? ''))

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}

function prettify(value: unknown): string {
  if (value === null || value === undefined) return 'null'
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function mcpLabel(item: CallRecord): string {
  return item.UpstreamName || item.UpstreamID || '未知 MCP'
}

function toolLabel(item: CallRecord): string {
  return item.ExposedName || item.OriginalName || '未知工具'
}

function apiKeyLabel(item: CallRecord): string {
  return item.APIKeyName || item.APIKeyID || '未知 API Key'
}

async function loadDetail(): Promise<void> {
  const id = Number.parseInt(detailId.value, 10)
  if (!Number.isFinite(id) || id <= 0) {
    errorMessage.value = '调用记录标识非法'
    return
  }
  loading.value = true
  errorMessage.value = ''
  try {
    record.value = await getCallRecord(id)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载调用详情失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadDetail)

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="调用详情" />

    <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">调用详情</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">查看单次工具调用的上下文与结果。</p>
      </div>
      <button
        type="button"
        class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        @click="router.push('/call-records')"
      >
        返回列表
      </button>
    </div>

    <div
      v-if="loading"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      加载中…
    </div>
    <p
      v-else-if="errorMessage !== ''"
      class="rounded-2xl border border-error-200 bg-error-50 px-5 py-4 text-sm text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ errorMessage }}
    </p>

    <template v-else-if="record !== null">
      <section :class="cardClass">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="truncate text-base font-semibold text-gray-800 dark:text-white/90">
                {{ toolLabel(record) }}
              </h3>
              <span
                class="rounded-full px-2.5 py-1 text-xs font-medium"
                :class="
                  record.Success
                    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
                    : 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
                "
              >
                {{ record.Success ? '成功' : '失败' }}
              </span>
            </div>
            <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">记录 ID：{{ record.ID }}</p>
          </div>
        </div>

        <div class="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">上游 MCP</p>
            <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-white/90">
              {{ mcpLabel(record) }}
            </p>
          </div>
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">API Key</p>
            <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-white/90">
              {{ apiKeyLabel(record) }}
            </p>
          </div>
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">调用时间</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              {{ formatDateTime(record.CalledAt) }}
            </p>
          </div>
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">耗时</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              {{ formatLatency(record.LatencyMS) }}
            </p>
          </div>
        </div>

        <p
          v-if="record.ErrorMessage !== ''"
          class="mt-5 rounded-xl bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ record.ErrorMessage }}
        </p>
      </section>

      <section class="mt-6 grid grid-cols-1 gap-5 xl:grid-cols-2">
        <article :class="cardClass" class="min-w-0">
          <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">入参</h3>
          <pre class="custom-scrollbar max-h-[560px] overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ prettify(record.RequestArgs) }}</code></pre>
        </article>
        <article :class="cardClass" class="min-w-0">
          <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">出参</h3>
          <pre class="custom-scrollbar max-h-[560px] overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ prettify(record.ResponseResult) }}</code></pre>
        </article>
      </section>
    </template>
  </AdminLayout>
</template>
