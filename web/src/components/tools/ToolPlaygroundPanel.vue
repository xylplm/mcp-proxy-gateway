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
  buildPlaygroundSchemaFields,
  initialSchemaFormValues,
  schemaArgsToFormValues,
  schemaFormDefaultArgs,
  schemaFormValuesToArgs,
  type PlaygroundSchemaField,
} from '@/utils/jsonSchemaPlayground'
import {
  buildPlaygroundCallRecordQuery,
  parsePlaygroundArgs,
  prettifyPlaygroundValue,
  type PlaygroundArgsParseResult,
} from '@/utils/toolPlayground'

const props = defineProps<{
  toolName: string
  inputSchema?: unknown
  initialApiKeyId?: string
}>()
const emit = defineEmits<{
  completed: []
}>()

const toast = useToast()

type InputMode = 'form' | 'json'

const apiKeys = ref<APIKey[]>([])
const selectedAPIKeyID = ref('')
const argsText = ref('{\n}')
const formValues = ref<Record<string, string>>({})
const inputMode = ref<InputMode>('json')
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
const schemaFields = computed<PlaygroundSchemaField[]>(() => buildPlaygroundSchemaFields(props.inputSchema))
const hasSchemaForm = computed(() => schemaFields.value.length > 0)
const usingFormMode = computed(() => inputMode.value === 'form' && hasSchemaForm.value)

watch(
  () => props.initialApiKeyId,
  (id) => {
    selectedAPIKeyID.value = id ?? ''
  },
  { immediate: true },
)

watch(
  () => [props.toolName, props.inputSchema] as const,
  () => {
    resetArgs()
    inputMode.value = hasSchemaForm.value ? 'form' : 'json'
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
  const parsed = buildArgs()
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
    emit('completed')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '调试调用失败')
  } finally {
    running.value = false
  }
}

function resetArgs(): void {
  const defaults = schemaFormDefaultArgs(schemaFields.value)
  argsText.value = prettifyPlaygroundValue(defaults)
  formValues.value = initialSchemaFormValues(schemaFields.value)
  argsError.value = ''
}

function switchInputMode(mode: InputMode): void {
  if (mode === inputMode.value) return
  if (mode === 'json') {
    const parsed = schemaFormValuesToArgs(schemaFields.value, formValues.value)
    if (parsed.ok) {
      argsText.value = prettifyPlaygroundValue(parsed.value)
      argsError.value = ''
    }
    inputMode.value = 'json'
    return
  }

  const parsed = parsePlaygroundArgs(argsText.value)
  if (parsed.ok) {
    formValues.value = schemaArgsToFormValues(schemaFields.value, parsed.value)
    argsError.value = ''
  }
  inputMode.value = 'form'
}

function buildArgs(): PlaygroundArgsParseResult {
  if (usingFormMode.value) {
    return schemaFormValuesToArgs(schemaFields.value, formValues.value)
  }
  return parsePlaygroundArgs(argsText.value)
}

function fieldInputType(field: PlaygroundSchemaField): string {
  if (field.kind === 'number' || field.kind === 'integer') return 'number'
  return 'text'
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

        <div class="mt-4">
          <span class="mb-2 flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-medium text-gray-500 dark:text-gray-400">入参</span>
            <span
              v-if="hasSchemaForm"
              class="inline-flex rounded-lg bg-gray-100 p-0.5 dark:bg-white/5"
            >
              <button
                type="button"
                class="rounded-md px-2.5 py-1 text-xs font-medium transition"
                :class="usingFormMode ? 'bg-white text-brand-700 shadow-sm dark:bg-gray-900 dark:text-brand-300' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
                @click="switchInputMode('form')"
              >
                表单
              </button>
              <button
                type="button"
                class="rounded-md px-2.5 py-1 text-xs font-medium transition"
                :class="!usingFormMode ? 'bg-white text-brand-700 shadow-sm dark:bg-gray-900 dark:text-brand-300' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
                @click="switchInputMode('json')"
              >
                JSON
              </button>
            </span>
          </span>

          <div
            v-if="usingFormMode"
            class="space-y-3 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.03]"
          >
            <label
              v-for="field in schemaFields"
              :key="field.name"
              class="block"
            >
              <span class="mb-1 flex flex-wrap items-center gap-1.5 text-xs font-medium text-gray-600 dark:text-gray-300">
                <span>{{ field.label }}</span>
                <span v-if="field.required" class="text-error-500">*</span>
                <span class="rounded-full bg-white px-1.5 py-0.5 text-[10px] text-gray-400 dark:bg-gray-900">
                  {{ field.kind }}
                </span>
              </span>
              <select
                v-if="field.enumOptions.length > 0"
                v-model="formValues[field.name]"
                class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
              >
                <option value="">不传此参数</option>
                <option v-for="option in field.enumOptions" :key="option.key" :value="option.key">
                  {{ option.label }}
                </option>
              </select>
              <select
                v-else-if="field.kind === 'boolean'"
                v-model="formValues[field.name]"
                class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
              >
                <option value="">不传此参数</option>
                <option value="true">true</option>
                <option value="false">false</option>
              </select>
              <textarea
                v-else-if="field.kind === 'json'"
                v-model="formValues[field.name]"
                spellcheck="false"
                class="custom-scrollbar h-24 w-full resize-y rounded-lg border border-gray-300 bg-white p-3 font-mono text-xs leading-5 text-gray-800 outline-none transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                placeholder="JSON 值"
              />
              <input
                v-else
                v-model="formValues[field.name]"
                :type="fieldInputType(field)"
                :step="field.kind === 'integer' ? '1' : 'any'"
                class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
              />
              <span v-if="field.description !== ''" class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                {{ field.description }}
              </span>
            </label>
          </div>

          <textarea
            v-else
            v-model="argsText"
            aria-label="入参 JSON"
            spellcheck="false"
            class="custom-scrollbar h-56 w-full resize-y rounded-xl border border-gray-300 bg-gray-950 p-3 font-mono text-xs leading-5 text-gray-100 outline-none transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 dark:border-gray-700"
          />
        </div>
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
