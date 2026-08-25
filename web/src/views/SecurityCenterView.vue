<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import AppSelect from '@/components/common/AppSelect.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { ArchiveIcon, RefreshIcon } from '@/icons'
import {
  exportSecurityBlocks,
  exportSecurityEvents,
  getSecuritySummary,
  listSecurityBlocks,
  listSecurityEvents,
  releaseSecurityBlock,
  type SecurityBlock,
  type SecurityBlockStatus,
  type SecurityEvent,
  type SecurityEventType,
  type SecuritySummary,
} from '@/api/security'
import { getSettings, type SecurityMode } from '@/api/settings'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const { confirm } = useConfirm()

const summary = ref<SecuritySummary | null>(null)
const blocks = ref<SecurityBlock[]>([])
const events = ref<SecurityEvent[]>([])
const securityMode = ref<SecurityMode>('monitor')
const blockStatus = ref<SecurityBlockStatus>('active')
const eventType = ref<SecurityEventType | ''>('')
const loading = ref(false)
const releasingId = ref('')
const exportingBlocks = ref(false)
const exportingEvents = ref(false)
const loadError = ref('')

const eventTypes = [
  { value: '', label: '全部事件' },
  { value: 'auth_failed', label: '鉴权失败' },
  { value: 'acl_denied', label: '来源拒绝' },
  { value: 'blocked', label: '自动封禁' },
  { value: 'released', label: '解除封禁' },
] as const

const blockStatusOptions = [
  { value: 'active', label: '当前封禁' },
  { value: 'released', label: '已解除' },
  { value: 'expired', label: '已过期' },
  { value: '', label: '全部状态' },
] as const

const modeLabel = computed(() => {
  switch (securityMode.value) {
    case 'enforce':
      return '自动封禁'
    case 'monitor':
      return '仅记录'
    case 'off':
      return '关闭'
    default:
      return securityMode.value
  }
})

const modeClass = computed(() => {
  switch (securityMode.value) {
    case 'enforce':
      return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    case 'monitor':
      return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  }
})

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const [settings, nextSummary, nextBlocks, nextEvents] = await Promise.all([
      getSettings(),
      getSecuritySummary(),
      listSecurityBlocks({ status: blockStatus.value, limit: 80 }),
      listSecurityEvents({ eventType: eventType.value, limit: 80 }),
    ])
    securityMode.value = settings.security.mode
    summary.value = nextSummary
    blocks.value = nextBlocks
    events.value = nextEvents
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载安全中心失败'
  } finally {
    loading.value = false
  }
}

async function releaseBlock(block: SecurityBlock): Promise<void> {
  if (releasingId.value !== '') return
  const ok = await confirm({
    title: '解除封禁',
    message: `确认解除 ${subjectLabel(block)} 的访问封禁？解除后会立即删除热封禁缓存。`,
    confirmText: '解除封禁',
    tone: 'warning',
  })
  if (!ok) return
  releasingId.value = block.ID
  try {
    await releaseSecurityBlock(block.ID)
    toast.success('封禁已解除')
    await load()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '解除封禁失败')
  } finally {
    releasingId.value = ''
  }
}

function exportFileName(prefix: string): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${prefix}-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.json`
}

function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

async function downloadBlocks(): Promise<void> {
  if (exportingBlocks.value) return
  exportingBlocks.value = true
  try {
    const blob = await exportSecurityBlocks({ status: blockStatus.value, limit: 200 })
    downloadBlob(blob, exportFileName('mpg-security-blocks'))
    toast.success('封禁记录已导出')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导出封禁记录失败')
  } finally {
    exportingBlocks.value = false
  }
}

async function downloadEvents(): Promise<void> {
  if (exportingEvents.value) return
  exportingEvents.value = true
  try {
    const blob = await exportSecurityEvents({ eventType: eventType.value, limit: 200 })
    downloadBlob(blob, exportFileName('mpg-security-events'))
    toast.success('安全事件已导出')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导出安全事件失败')
  } finally {
    exportingEvents.value = false
  }
}

function eventLabel(type: string): string {
  switch (type) {
    case 'auth_failed':
      return '鉴权失败'
    case 'acl_denied':
      return '来源拒绝'
    case 'blocked':
      return '自动封禁'
    case 'released':
      return '解除封禁'
    default:
      return type
  }
}

function reasonLabel(reason: string): string {
  switch (reason) {
    case 'missing_key':
      return '缺少 API Key'
    case 'invalid_key':
      return 'API Key 无效'
    case 'acl_denied':
      return '来源不在白名单'
    default:
      return reason || '未标注'
  }
}

function subjectTypeLabel(type: string): string {
  switch (type) {
    case 'ip':
      return '来源 IP'
    case 'key_fingerprint':
      return '疑似 Key 指纹'
    case 'api_key_ip':
      return 'API Key + 来源'
    default:
      return type
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '封禁中'
    case 'released':
      return '已解除'
    case 'expired':
      return '已过期'
    default:
      return status
  }
}

function statusClass(status: string): string {
  const base = 'rounded-full px-2.5 py-1 text-xs font-medium'
  switch (status) {
    case 'active':
      return `${base} bg-error-50 text-error-700 dark:bg-error-500/10 dark:text-error-400`
    case 'released':
      return `${base} bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-400`
    case 'expired':
      return `${base} bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400`
    default:
      return `${base} bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400`
  }
}

function eventClass(type: string): string {
  const base = 'rounded-full px-2.5 py-1 text-xs font-medium'
  switch (type) {
    case 'blocked':
      return `${base} bg-error-50 text-error-700 dark:bg-error-500/10 dark:text-error-400`
    case 'acl_denied':
      return `${base} bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400`
    case 'auth_failed':
      return `${base} bg-gray-100 text-gray-700 dark:bg-white/5 dark:text-gray-300`
    case 'released':
      return `${base} bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400`
    default:
      return `${base} bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400`
  }
}

function subjectLabel(item: SecurityBlock | SecurityEvent): string {
  if (item.SubjectType === 'api_key_ip') {
    return `${item.APIKeyPrefix || item.APIKeyID || 'API Key'} · ${item.ClientIP || item.Subject}`
  }
  if (item.SubjectType === 'key_fingerprint') return item.KeyFingerprint || item.Subject
  return item.Subject || item.ClientIP || '未知来源'
}

function formatTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatRemain(value?: string | null): string {
  if (!value) return '永久'
  const ms = new Date(value).getTime() - Date.now()
  if (!Number.isFinite(ms) || ms <= 0) return '已到期'
  const minutes = Math.ceil(ms / 60000)
  if (minutes < 60) return `${minutes} 分钟后`
  const hours = Math.ceil(minutes / 60)
  if (hours < 24) return `${hours} 小时后`
  return `${Math.ceil(hours / 24)} 天后`
}

function eventMeta(event: SecurityEvent): string {
  const parts = [event.Method, event.Path].filter((item) => item !== '')
  return parts.length > 0 ? parts.join(' ') : '—'
}

onMounted(load)

const cardClass = 'rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]'
const iconButtonClass = 'inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="安全中心" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div>
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">安全中心</h2>
          <span :class="['rounded-full px-2.5 py-1 text-xs font-medium', modeClass]">{{ modeLabel }}</span>
        </div>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          查看对外 MCP 入口的异常访问、自动封禁和解除记录。
        </p>
      </div>
      <div class="flex items-center gap-2">
        <router-link
          to="/settings"
          class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        >
          调整策略
        </router-link>
        <button
          v-tooltip:bottom-end="'刷新安全中心'"
          type="button"
          :class="iconButtonClass"
          :disabled="loading"
          aria-label="刷新安全中心"
          @click="load"
        >
          <RefreshIcon class="h-5 w-5" :class="loading ? 'animate-spin' : ''" />
        </button>
      </div>
    </div>

    <p
      v-if="loadError !== ''"
      class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ loadError }}
    </p>

    <div class="mb-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">当前封禁</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">{{ summary?.ActiveBlocks ?? 0 }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">24 小时鉴权失败</p>
        <p class="mt-2 text-2xl font-semibold text-error-600 dark:text-error-400">{{ summary?.AuthFailures24h ?? 0 }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">24 小时来源拒绝</p>
        <p class="mt-2 text-2xl font-semibold text-warning-700 dark:text-warning-400">{{ summary?.ACLDenies24h ?? 0 }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">24 小时风险对象</p>
        <p class="mt-2 text-2xl font-semibold text-brand-600 dark:text-brand-400">{{ summary?.HighRiskSubjects24h ?? 0 }}</p>
      </section>
    </div>

    <div class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
      <section :class="cardClass">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">访问封禁</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">临时封禁记录，可手动解除当前封禁。</p>
          </div>
          <div class="flex items-center gap-2">
            <AppSelect
              v-model="blockStatus"
              :options="blockStatusOptions"
              class="w-40"
              aria-label="封禁状态"
              @change="load"
            />
            <button
              v-tooltip:bottom-end="'导出当前封禁记录'"
              type="button"
              :class="iconButtonClass"
              :disabled="loading || exportingBlocks"
              aria-label="导出当前封禁记录"
              @click="downloadBlocks"
            >
              <ArchiveIcon class="h-5 w-5" />
            </button>
          </div>
        </div>

        <div v-if="loading && blocks.length === 0" class="py-10 text-center text-sm text-gray-400">加载中...</div>
        <div v-else-if="blocks.length === 0" class="py-10 text-center text-sm text-gray-400">暂无封禁记录</div>
        <div v-else class="space-y-3">
          <article
            v-for="block in blocks"
            :key="block.ID"
            class="rounded-lg border border-gray-100 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span :class="statusClass(block.Status)">{{ statusLabel(block.Status) }}</span>
                  <span class="rounded-full bg-white px-2.5 py-1 text-xs text-gray-500 dark:bg-white/5 dark:text-gray-400">
                    {{ subjectTypeLabel(block.SubjectType) }}
                  </span>
                </div>
                <p class="mt-2 break-all font-mono text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ subjectLabel(block) }}
                </p>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ reasonLabel(block.Reason) }} · {{ block.FailureCount }} 次 · {{ formatTime(block.CreatedAt) }}
                </p>
              </div>
              <button
                v-if="block.Status === 'active'"
                type="button"
                class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="releasingId === block.ID"
                @click="releaseBlock(block)"
              >
                {{ releasingId === block.ID ? '解除中...' : '解除' }}
              </button>
            </div>
            <div class="mt-3 grid grid-cols-1 gap-2 text-xs text-gray-500 sm:grid-cols-2 dark:text-gray-400">
              <p>到期：{{ formatTime(block.BlockedUntil) }}</p>
              <p>剩余：{{ formatRemain(block.BlockedUntil) }}</p>
              <p v-if="block.APIKeyID !== ''" class="break-all">API Key：{{ block.APIKeyPrefix || block.APIKeyID }}</p>
              <p v-if="block.KeyFingerprint !== ''" class="break-all">指纹：{{ block.KeyFingerprint }}</p>
            </div>
          </article>
        </div>
      </section>

      <section :class="cardClass">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">安全事件</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按时间倒序展示最近的异常访问与处置动作。</p>
          </div>
          <div class="flex items-center gap-2">
            <AppSelect
              v-model="eventType"
              :options="eventTypes"
              class="w-40"
              aria-label="安全事件类型"
              @change="load"
            />
            <button
              v-tooltip:bottom-end="'导出当前安全事件'"
              type="button"
              :class="iconButtonClass"
              :disabled="loading || exportingEvents"
              aria-label="导出当前安全事件"
              @click="downloadEvents"
            >
              <ArchiveIcon class="h-5 w-5" />
            </button>
          </div>
        </div>

        <div v-if="loading && events.length === 0" class="py-10 text-center text-sm text-gray-400">加载中...</div>
        <div v-else-if="events.length === 0" class="py-10 text-center text-sm text-gray-400">暂无安全事件</div>
        <div v-else class="space-y-3">
          <article
            v-for="event in events"
            :key="event.ID"
            class="rounded-lg border border-gray-100 p-3 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <span :class="eventClass(event.EventType)">{{ eventLabel(event.EventType) }}</span>
                  <span class="rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-500 dark:bg-white/5 dark:text-gray-400">
                    {{ subjectTypeLabel(event.SubjectType) }}
                  </span>
                </div>
                <p class="mt-2 break-all font-mono text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ subjectLabel(event) }}
                </p>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ reasonLabel(event.Reason) }} · {{ event.Count }} 次 · {{ formatTime(event.CreatedAt) }}
                </p>
              </div>
              <span class="shrink-0 text-xs text-gray-400 dark:text-gray-500">#{{ event.ID }}</span>
            </div>
            <div class="mt-3 grid grid-cols-1 gap-2 text-xs text-gray-500 sm:grid-cols-2 dark:text-gray-400">
              <p class="break-all">请求：{{ eventMeta(event) }}</p>
              <p class="break-all">来源：{{ event.ClientIP || '—' }}</p>
              <p v-if="event.APIKeyID !== ''" class="break-all">API Key：{{ event.APIKeyPrefix || event.APIKeyID }}</p>
              <p v-if="event.KeyFingerprint !== ''" class="break-all">指纹：{{ event.KeyFingerprint }}</p>
              <p v-if="event.UserAgent !== ''" class="break-all sm:col-span-2">UA：{{ event.UserAgent }}</p>
            </div>
          </article>
        </div>
      </section>
    </div>
  </AdminLayout>
</template>
