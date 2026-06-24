<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { listAPIKeys, type APIKey } from '@/api/apikeys'
import {
  invokeToolPlayground,
  type ToolPlaygroundResponse,
} from '@/api/tools'
import { useToast } from '@/composables/useToast'
import {
  buildPlaygroundCallRecordQuery,
  parsePlaygroundArgs,
  prettifyPlaygroundValue,
} from '@/utils/toolPlayground'

const props = defineProps<{
  toolName: string
  initialApiKeyId?: string
}>()

const toast = useToast()

const apiKeys = ref<APIKey[]>([])
const selectedAPIKeyID = ref('')
const argsText = ref('{\n}')
const running = ref(false)
const loadingKeys = ref(false)
const loadKeyError = ref('')
const argsError = ref('')
const result = ref<ToolPlaygroundResponse | null>(null)

const runnable = computed(() => props.toolName.trim() !== '' && !running.value)
const selectedAPIKeyLabel = computed(() => {
  if (selectedAPIKeyID.value === '') return '全局视角'
  const key = apiKeys.value.find((item) => item.id === selectedAPIKeyID.value)
  return key?.name || selectedAPIKeyID.value
})
const callRecordQuery = computed(() => buildPlaygroundCallRecordQuery(props.toolName))

watch(
  () => props.initialApiKeyId,
  (id) => {
    selectedAPIKeyID.value = id ?? ''
  },
  { immediate: true },
)

onMounted(() => {
  void loadAPIKeys()
})

async function loadAPIKeys(): Promise<void> {
  loadingKeys.value = true
  loadKeyError.value = ''
  try {
    apiKeys.value = await listAPIKeys()
  } catch (err) {
    loadKeyError.value = err instanceof Error ? err.message : '加载 API Key 失败'
  } finally {
    loadingKeys.value = false
  }
}

async function runPlayground(): Promise<void> {
  if (!runnable.value) return
  const parsed = parsePlaygroundArgs(argsText.value)
  if (!parsed.ok) {
    argsError.value = parsed.error
    return
  }
  argsError.value = ''
  running.value = true
  result.value = null
  try {
    result.value = await invokeToolPlayground({
      apiKeyId: selectedAPIKeyID.value || undefined,
      name: props.toolName,
      args: parsed.value,
    })
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '调试调用失败')
  } finally {
    running.value = false
  }
}

function resetArgs(): void {
  argsText.value = '{\n}'
  argsError.value = ''
}

function resultStatusLabel(item: ToolPlaygroundResponse): string {
  if (item.success) return '成功'
  if (item.isError) return '上游错误'
  return '调用失败'
}

function resultStatusClass(item: ToolPlaygroundResponse): string {
  if (item.success) {
    return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
  }
  if (item.isError) {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
  }
  return 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}
</script>

<template>
  <section class="mt-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.02]">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
      <div class="min-w-0">
        <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">调试调用</h4>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
          按当前聚合路由发起一次真实调用，结果会进入调用记录。
        </p>
      </div>
      <span class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-white/5 dark:text-gray-400">
        {{ selectedAPIKeyLabel }}
      </span>
    </div>

    <div class="mt-4 grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div class="min-w-0">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">调用视角</span>
            <select
              v-model="selectedAPIKeyID"
              class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
            >
              <option value="">全局视角</option>
              <option v-for="key in apiKeys" :key="key.id" :value="key.id">
                {{ key.name }}
              </option>
            </select>
          </label>
          <button
            type="button"
            class="h-10 rounded-lg border border-gray-300 px-4 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="loadingKeys"
            @click="loadAPIKeys"
          >
            刷新 Key
          </button>
        </div>
        <p v-if="loadKeyError !== ''" class="mt-2 text-xs text-error-600 dark:text-error-400">
          {{ loadKeyError }}
        </p>

        <label class="mt-4 block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">入参 JSON</span>
          <textarea
            v-model="argsText"
            spellcheck="false"
            class="custom-scrollbar h-56 w-full resize-y rounded-xl border border-gray-300 bg-gray-950 p-3 font-mono text-xs leading-5 text-gray-100 outline-none transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 dark:border-gray-700"
          />
        </label>
        <p v-if="argsError !== ''" class="mt-2 text-xs text-error-600 dark:text-error-400">
          {{ argsError }}
        </p>

        <div class="mt-3 flex flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            class="h-9 rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="resetArgs"
          >
            重置入参
          </button>
          <button
            type="button"
            class="h-9 rounded-lg bg-brand-500 px-4 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
            :disabled="!runnable"
            @click="runPlayground"
          >
            {{ running ? '调用中…' : '执行调用' }}
          </button>
        </div>
      </div>

      <div class="min-w-0">
        <div
          v-if="result === null"
          class="flex h-full min-h-72 items-center justify-center rounded-xl border border-dashed border-gray-300 px-5 py-10 text-center text-sm text-gray-400 dark:border-gray-700"
        >
          调用后将在这里显示原始结果。
        </div>
        <div v-else class="rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.03]">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-2">
              <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="resultStatusClass(result)">
                {{ resultStatusLabel(result) }}
              </span>
              <span class="rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-white/5 dark:text-gray-400">
                {{ formatLatency(result.latencyMs) }}
              </span>
            </div>
            <span v-if="result.errorCode" class="font-mono text-xs text-error-600 dark:text-error-400">
              {{ result.errorCode }}
            </span>
          </div>
          <RouterLink
            class="mt-3 inline-flex rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :to="{ name: 'CallRecords', query: callRecordQuery }"
          >
            查看调用记录
          </RouterLink>
          <p v-if="result.error" class="mt-3 rounded-lg bg-error-50 px-3 py-2 text-xs leading-5 text-error-600 dark:bg-error-500/10 dark:text-error-400">
            {{ result.error }}
          </p>
          <pre class="custom-scrollbar mt-3 h-72 overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100 xl:h-[320px]"><code>{{ prettifyPlaygroundValue(result.content ?? null) }}</code></pre>
        </div>
      </div>
    </div>
  </section>
</template>
