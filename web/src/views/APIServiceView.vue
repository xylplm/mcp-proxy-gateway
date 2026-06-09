<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  BoxCubeIcon,
  CheckIcon,
  InfoCircleIcon,
  PlugInIcon,
  RefreshIcon,
  UserGroupIcon,
} from '@/icons'
import { listAPIKeys } from '@/api/apikeys'
import { getHealth, type DetailHealthReport } from '@/api/health'
import { extractAPIError, getSettings, updateSettings, type YAMLConfig } from '@/api/settings'

const loading = ref(false)
const refreshing = ref(false)
const saving = ref(false)
const loadError = ref('')
const formError = ref('')
const toast = ref('')
const copyError = ref('')
const copiedKey = ref('')

const settings = ref<YAMLConfig | null>(null)
const health = ref<DetailHealthReport | null>(null)
const apiKeyTotal = ref(0)
const apiKeyEnabled = ref(0)

const xiaozhiEnabled = ref(false)
const xiaozhiEndpoint = ref('')
const smartDiscoveryLimit = ref(50)
const fieldErrors = reactive<Record<string, string>>({})

const origin = computed(() => {
  if (typeof window === 'undefined') return ''
  return window.location.origin
})

const wsOrigin = computed(() => {
  if (typeof window === 'undefined') return ''
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}`
})

const endpoints = computed(() => [
  {
    key: 'sse',
    name: 'SSE',
    badge: '事件流',
    address: `${origin.value}/mcp/sse`,
    desc: '适合仍使用 SSE 传输的 MCP 客户端。',
  },
  {
    key: 'http',
    name: 'Streamable HTTP',
    badge: '推荐',
    address: `${origin.value}/mcp/http`,
    desc: '适合支持新版 HTTP 传输的客户端。',
  },
  {
    key: 'ws',
    name: 'WebSocket',
    badge: '长连接',
    address: `${wsOrigin.value}/mcp/ws`,
    desc: '适合需要稳定双向通道的客户端。',
  },
])

const enabledUpstreams = computed(
  () => health.value?.upstreams.filter((item) => item.enabled).length ?? 0,
)
const availableUpstreams = computed(
  () =>
    health.value?.upstreams.filter((item) => item.enabled && item.state === 'available').length ??
    0,
)
const dependencyOK = computed(
  () => health.value?.dependencies.filter((item) => item.status === 'ok').length ?? 0,
)
const currentModeLabel = computed(() =>
  settings.value?.mcp_api.mode === 'full' ? '全量模式' : '智能模式',
)
const xiaozhiStatusLabel = computed(() => {
  if (!settings.value?.xiaozhi.enabled) return '未启用'
  if (health.value?.xiaozhi?.connected) return '已连接'
  return '等待连接'
})
const xiaozhiStatusClass = computed(() => {
  if (!settings.value?.xiaozhi.enabled) {
    return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  }
  if (health.value?.xiaozhi?.connected) {
    return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
  }
  return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
})
const systemStatusLabel = computed(() => {
  if (health.value === null) return '加载中'
  return health.value.status === 'ok' ? '运行正常' : '部分异常'
})
const systemStatusClass = computed(() =>
  health.value?.status === 'ok'
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400',
)

function syncForm(cfg: YAMLConfig): void {
  xiaozhiEnabled.value = cfg.xiaozhi.enabled
  xiaozhiEndpoint.value = cfg.xiaozhi.endpoint
  smartDiscoveryLimit.value = cfg.mcp_api.smart_discovery_limit
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : '数据加载失败，请稍后重试'
}

function clearFieldErrors(): void {
  formError.value = ''
  for (const key of Object.keys(fieldErrors)) {
    delete fieldErrors[key]
  }
}

function showToast(message: string): void {
  toast.value = message
  setTimeout(() => {
    if (toast.value === message) toast.value = ''
  }, 2500)
}

async function loadAPIService(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [cfg, report, keys] = await Promise.all([getSettings(), getHealth(), listAPIKeys()])
    settings.value = cfg
    health.value = report
    apiKeyTotal.value = keys.length
    apiKeyEnabled.value = keys.filter((item) => item.enabled).length
    syncForm(cfg)
  } catch (err) {
    loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

async function refreshStatus(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  copyError.value = ''
  try {
    const [report, keys] = await Promise.all([getHealth(), listAPIKeys()])
    health.value = report
    apiKeyTotal.value = keys.length
    apiKeyEnabled.value = keys.filter((item) => item.enabled).length
  } catch (err) {
    copyError.value = errorMessage(err)
  } finally {
    refreshing.value = false
  }
}

async function saveAPISettings(): Promise<void> {
  if (settings.value === null || saving.value) return
  clearFieldErrors()
  saving.value = true
  const next: YAMLConfig = {
    ...settings.value,
    mcp_api: {
      ...settings.value.mcp_api,
      smart_discovery_limit: smartDiscoveryLimit.value,
    },
    xiaozhi: {
      enabled: xiaozhiEnabled.value,
      endpoint: xiaozhiEndpoint.value.trim(),
    },
  }
  try {
    settings.value = await updateSettings(next)
    syncForm(settings.value)
    await refreshStatus()
    showToast('API 服务设置已保存')
  } catch (err) {
    const body = extractAPIError(err)
    if (body?.fields) {
      for (const [key, value] of Object.entries(body.fields)) {
        fieldErrors[key] = value
      }
    }
    formError.value = body?.message ?? errorMessage(err)
  } finally {
    saving.value = false
  }
}

async function copyEndpoint(key: string, value: string): Promise<void> {
  copyError.value = ''
  try {
    await navigator.clipboard.writeText(value)
    copiedKey.value = key
    setTimeout(() => {
      if (copiedKey.value === key) copiedKey.value = ''
    }, 1800)
  } catch {
    copyError.value = '复制失败，请手动选择地址'
  }
}

onMounted(loadAPIService)

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
const hintClass = 'mt-1 text-xs text-gray-400 dark:text-gray-500'
const errClass = 'mt-1 text-xs text-error-500'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="API 服务" />

    <div
      v-if="loadError !== ''"
      class="mb-6 flex items-center justify-between gap-3 rounded-lg bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      <span>{{ loadError }}</span>
      <button
        type="button"
        class="rounded-md border border-error-200 px-3 py-1 text-xs font-medium hover:bg-error-100 dark:border-error-500/30 dark:hover:bg-error-500/10"
        @click="loadAPIService"
      >
        重试
      </button>
    </div>

    <section :class="cardClass" class="mb-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">服务概览</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            外部客户端接入、鉴权与小智连接状态集中在这里查看和调整。
          </p>
        </div>
        <button
          v-tooltip:bottom-end="'刷新状态'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
          :disabled="loading || refreshing"
          aria-label="刷新状态"
          @click="refreshStatus"
        >
          <RefreshIcon class="h-5 w-5" :class="refreshing ? 'animate-spin' : ''" />
        </button>
      </div>

      <p
        v-if="toast !== ''"
        class="mt-4 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
      >
        {{ toast }}
      </p>
      <p
        v-if="copyError !== ''"
        class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ copyError }}
      </p>

      <div class="mt-5 grid grid-cols-1 gap-4 sm:grid-cols-2 2xl:grid-cols-4">
        <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
            <BoxCubeIcon class="h-5 w-5" />
          </span>
          <p class="mt-4 text-xs text-gray-400 dark:text-gray-500">系统状态</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">{{ systemStatusLabel }}</p>
          <span class="mt-3 inline-flex rounded-full px-2.5 py-1 text-xs font-medium" :class="systemStatusClass">
            {{ dependencyOK }}/{{ health?.dependencies.length ?? 0 }} 依赖正常
          </span>
        </div>
        <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400">
            <PlugInIcon class="h-5 w-5" />
          </span>
          <p class="mt-4 text-xs text-gray-400 dark:text-gray-500">上游连接</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">
            {{ availableUpstreams }}/{{ enabledUpstreams }} 可用
          </p>
          <span class="mt-3 inline-flex rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-white/5 dark:text-gray-400">
            已启用上游
          </span>
        </div>
        <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-warning-50 text-warning-600 dark:bg-warning-500/10 dark:text-warning-400">
            <UserGroupIcon class="h-5 w-5" />
          </span>
          <p class="mt-4 text-xs text-gray-400 dark:text-gray-500">API Key</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">
            {{ apiKeyEnabled }}/{{ apiKeyTotal }} 可用
          </p>
          <router-link to="/apikeys" class="mt-3 inline-flex text-xs font-medium text-brand-600 hover:text-brand-700 dark:text-brand-400">
            管理密钥
          </router-link>
        </div>
        <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-light-50 text-blue-light-700 dark:bg-blue-light-500/10 dark:text-blue-light-400">
            <InfoCircleIcon class="h-5 w-5" />
          </span>
          <p class="mt-4 text-xs text-gray-400 dark:text-gray-500">对外模式</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">{{ currentModeLabel }}</p>
          <router-link to="/" class="mt-3 inline-flex text-xs font-medium text-brand-600 hover:text-brand-700 dark:text-brand-400">
            前往首页切换
          </router-link>
        </div>
      </div>
    </section>

    <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.25fr)_minmax(360px,0.75fr)]">
      <section :class="cardClass">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">对外接口地址</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              外部 MCP 客户端使用这些地址连接本系统。
            </p>
          </div>
          <span class="rounded-full bg-brand-50 px-2.5 py-1 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
            需要 API Key
          </span>
        </div>

        <div class="mt-5 grid grid-cols-1 gap-4 2xl:grid-cols-3">
          <article
            v-for="item in endpoints"
            :key="item.key"
            class="flex min-h-52 flex-col rounded-xl border border-gray-200 p-4 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex items-start justify-between gap-3">
              <div>
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.name }}</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.desc }}</p>
              </div>
              <span class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-white/5 dark:text-gray-400">
                {{ item.badge }}
              </span>
            </div>

            <div class="mt-4 rounded-lg bg-gray-50 p-3 dark:bg-gray-900/60">
              <p class="break-all font-mono text-xs leading-5 text-gray-700 dark:text-gray-200">
                {{ item.address }}
              </p>
            </div>

            <button
              v-tooltip:bottom="copiedKey === item.key ? '已复制' : '复制地址'"
              type="button"
              class="mt-auto inline-flex h-10 items-center justify-center gap-2 rounded-lg border border-gray-200 px-3 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
              :aria-label="`复制 ${item.name} 地址`"
              @click="copyEndpoint(item.key, item.address)"
            >
              <CheckIcon v-if="copiedKey === item.key" class="h-4 w-4" />
              <span>{{ copiedKey === item.key ? '已复制' : '复制地址' }}</span>
            </button>
          </article>
        </div>

        <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">鉴权方式</h4>
          <div class="mt-3 grid grid-cols-1 gap-3 lg:grid-cols-3">
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">请求头</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">X-API-Key: &lt;API Key&gt;</p>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">Bearer</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">Authorization: Bearer &lt;API Key&gt;</p>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">查询参数</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">?api_key=&lt;API Key&gt;</p>
            </div>
          </div>
        </div>
      </section>

      <section :class="cardClass">
        <form class="flex h-full flex-col" @submit.prevent="saveAPISettings">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">小智接入</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                网关主动连接小智 MCP 接入点并提供聚合工具。
              </p>
            </div>
            <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="xiaozhiStatusClass">
              {{ xiaozhiStatusLabel }}
            </span>
          </div>

          <p
            v-if="formError !== ''"
            class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
          >
            {{ formError }}
          </p>

          <div class="mt-5 flex flex-col gap-5">
            <div class="flex items-center justify-between gap-4 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
              <div>
                <label :class="labelClass">启用小智接入</label>
                <p class="text-xs text-gray-400 dark:text-gray-500">保存后立即应用到当前网关进程。</p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="xiaozhiEnabled"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="xiaozhiEnabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="xiaozhiEnabled = !xiaozhiEnabled"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="xiaozhiEnabled ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>

            <div>
              <label :class="labelClass">接入点地址</label>
              <input
                v-model="xiaozhiEndpoint"
                type="text"
                placeholder="wss://example.com/mcp"
                :disabled="!xiaozhiEnabled"
                :class="[inputClass, !xiaozhiEnabled ? 'opacity-60' : '']"
              />
              <p :class="hintClass">启用时需填写 ws:// 或 wss:// 地址。</p>
              <p v-if="fieldErrors['xiaozhi.endpoint']" :class="errClass">
                {{ fieldErrors['xiaozhi.endpoint'] }}
              </p>
            </div>

            <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
              <p class="text-xs text-gray-400 dark:text-gray-500">当前连接</p>
              <p class="mt-1 break-all text-sm font-medium text-gray-800 dark:text-white/90">
                {{ health?.xiaozhi?.endpoint || settings?.xiaozhi.endpoint || '未配置' }}
              </p>
            </div>

            <details class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
              <summary class="cursor-pointer text-sm font-medium text-gray-800 dark:text-white/90">
                高级设置
              </summary>
              <div class="mt-4">
                <label :class="labelClass">智能模式默认返回工具数</label>
                <input
                  v-model.number="smartDiscoveryLimit"
                  type="number"
                  min="1"
                  max="200"
                  :class="inputClass"
                />
                <p :class="hintClass">仅智能模式生效，范围 1 至 200。</p>
                <p v-if="fieldErrors['mcp_api.smart_discovery_limit']" :class="errClass">
                  {{ fieldErrors['mcp_api.smart_discovery_limit'] }}
                </p>
              </div>
            </details>
          </div>

          <div class="mt-6 flex justify-end">
            <button
              type="submit"
              class="rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
              :disabled="saving || settings === null"
            >
              {{ saving ? '保存中…' : '保存 API 服务设置' }}
            </button>
          </div>
        </form>
      </section>
    </div>
  </AdminLayout>
</template>
