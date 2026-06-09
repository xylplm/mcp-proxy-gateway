<script setup lang="ts">
/**
 * 上游 MCP 创建/编辑抽屉（TailAdmin 风格 drawer + form）。
 *
 * 覆盖 Req 2.1（创建）、2.4（编辑）、14.7-14.11（基于模板预填充与占位参数填写校验）。
 *
 * 两种来源：
 * - 手动新建/编辑：按传输类型渲染对应连接参数字段（stdio→command/args；其余→url）。
 * - 模板预填充（prefill）：传输类型固定、展示预设连接参数，并将占位参数渲染为待输入字段，
 *   必填占位参数标记为必填（Req 14.7）。提交前在前端注入占位取值到预设参数的 ${name} 引用，
 *   并对 url 合法性、必填项、长度/正则等做基础校验（Req 14.8-14.11），secret 类占位作为凭证。
 *
 * 注意：占位注入与基础校验在前端进行（后端的「基于模板创建」尚未暴露 REST 路由），
 * 最终权威校验仍由后端 createUpstream 完成。
 */
import { computed, reactive, ref, watch } from 'vue'
import {
  createUpstream,
  updateUpstream,
  TRANSPORT_OPTIONS,
  type ConnParams,
  type TransportType,
  type Upstream,
  type UpstreamConfigRequest,
} from '@/api/upstreams'
import type { PrefillForm, Placeholder } from '@/api/templates'
import { ApiError } from '@/api/request'

const props = defineProps<{
  /** 是否打开抽屉。 */
  open: boolean
  /** 编辑目标；为 null 表示新建。 */
  upstream: Upstream | null
  /** 模板预填充数据；仅新建场景下可能存在。 */
  prefill: PrefillForm | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

/** 是否为编辑模式。 */
const isEdit = computed(() => props.upstream !== null)

/** 表单标题。 */
const title = computed(() => {
  if (isEdit.value) return '编辑上游 MCP'
  if (props.prefill !== null) return `基于模板创建：${props.prefill.name}`
  return '新建上游 MCP'
})

/** 表单基础字段模型。 */
const form = reactive<{
  name: string
  transport: TransportType
  command: string
  args: string
  url: string
  credential: string
  enabled: boolean
  autoSync: boolean
  sortOrder: number
}>({
  name: '',
  transport: 'stdio',
  command: '',
  args: '',
  url: '',
  credential: '',
  enabled: true,
  autoSync: true,
  sortOrder: 0,
})

/** 模板占位参数取值（key = placeholder.name）。 */
const placeholderValues = reactive<Record<string, string>>({})

/** 当前模板占位参数定义（来自 prefill）。 */
const placeholders = ref<Placeholder[]>([])

/** 模板预设连接参数（只读展示用）。 */
const presetParams = ref<ConnParams>({})

/** 是否来自模板（决定渲染占位字段而非手动连接参数字段）。 */
const fromTemplate = computed(() => props.prefill !== null && !isEdit.value)

/** 提交中标志（防重复提交）。 */
const submitting = ref(false)

/** 字段级错误信息（key = 字段名 / 占位参数名）。 */
const fieldErrors = reactive<Record<string, string>>({})

/** 顶层错误提示（如后端返回的整体错误）。 */
const formError = ref('')

/** 重置全部字段错误。 */
function clearErrors(): void {
  for (const k of Object.keys(fieldErrors)) delete fieldErrors[k]
  formError.value = ''
}

/** 将 args 字符串（逗号或换行分隔）解析为字符串数组。 */
function parseArgs(raw: string): string[] {
  return raw
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter((s) => s !== '')
}

/** 依据来源数据（编辑/模板/空白）初始化表单。 */
function resetForm(): void {
  clearErrors()
  for (const k of Object.keys(placeholderValues)) delete placeholderValues[k]

  if (props.upstream !== null) {
    // 编辑模式：回填现有配置。
    const cfg = props.upstream.config
    form.name = cfg.name
    form.transport = cfg.transport
    form.command = typeof cfg.connParams.command === 'string' ? cfg.connParams.command : ''
    form.args = Array.isArray(cfg.connParams.args) ? cfg.connParams.args.join('\n') : ''
    form.url = typeof cfg.connParams.url === 'string' ? cfg.connParams.url : ''
    form.credential = ''
    form.enabled = cfg.enabled
    form.autoSync = cfg.autoSync
    form.sortOrder = cfg.sortOrder
    placeholders.value = []
    presetParams.value = {}
    return
  }

  if (props.prefill !== null) {
    // 模板预填充模式（Req 14.7）。
    const pf = props.prefill
    form.name = pf.name
    form.transport = pf.transport
    form.command = ''
    form.args = ''
    form.url = ''
    form.credential = ''
    form.enabled = true
    form.autoSync = true
    form.sortOrder = 0
    placeholders.value = pf.placeholders ?? []
    presetParams.value = pf.presetParams ?? {}
    for (const ph of placeholders.value) {
      placeholderValues[ph.name] = ''
    }
    return
  }

  // 空白新建。
  form.name = ''
  form.transport = 'stdio'
  form.command = ''
  form.args = ''
  form.url = ''
  form.credential = ''
  form.enabled = true
  form.autoSync = true
  form.sortOrder = 0
  placeholders.value = []
  presetParams.value = {}
}

// 抽屉打开时（或来源切换时）重置表单。
watch(
  () => [props.open, props.upstream, props.prefill],
  () => {
    if (props.open) resetForm()
  },
  { immediate: true },
)

/** 占位参数显示标签：优先 label，回退到 name。 */
function placeholderLabel(ph: Placeholder): string {
  return ph.label?.trim() ? ph.label : ph.name
}

/** 占位参数是否以密码框承载（secret 类，不回显，Req 14.1）。 */
function isSecret(ph: Placeholder): boolean {
  return ph.rule?.kind === 'secret'
}

/**
 * 校验单个占位取值，返回错误文本（空串表示通过）。
 * 镜像后端 build.go 的基础校验：必填、长度、url、int、正则（Req 14.8-14.10）。
 */
function validatePlaceholder(ph: Placeholder, raw: string): string {
  const val = raw.trim()
  if (val === '') {
    return ph.required ? `缺少必填参数「${placeholderLabel(ph)}」` : ''
  }
  const rule = ph.rule ?? { kind: 'string' as const }
  const len = [...val].length
  if (rule.minLen && len < rule.minLen) {
    return `${placeholderLabel(ph)}长度不能小于 ${rule.minLen} 个字符`
  }
  if (rule.maxLen && len > rule.maxLen) {
    return `${placeholderLabel(ph)}长度不能超过 ${rule.maxLen} 个字符`
  }
  if (rule.kind === 'url' && !isValidURL(val)) {
    return `${placeholderLabel(ph)}不是合法 URL`
  }
  if (rule.kind === 'int' && !/^-?\d+$/.test(val)) {
    return `${placeholderLabel(ph)}必须为整数`
  }
  if (rule.pattern) {
    try {
      const re = new RegExp(`^(?:${rule.pattern})$`)
      if (!re.test(val)) return `${placeholderLabel(ph)}格式不符合要求`
    } catch {
      return `${placeholderLabel(ph)}的校验规则非法`
    }
  }
  return ''
}

/** 判断字符串是否为带协议与主机的合法绝对 URL。 */
function isValidURL(s: string): boolean {
  try {
    const u = new URL(s)
    return u.protocol !== '' && u.host !== ''
  } catch {
    return false
  }
}

/** 收集预设值中通过 ${name} 引用的占位名。 */
function collectRefs(value: unknown, names: Set<string>): void {
  if (typeof value === 'string') {
    const re = /\$\{([a-zA-Z0-9_]+)\}/g
    let m: RegExpExecArray | null
    while ((m = re.exec(value)) !== null) names.add(m[1])
  } else if (Array.isArray(value)) {
    value.forEach((item) => collectRefs(item, names))
  } else if (value !== null && typeof value === 'object') {
    Object.values(value as Record<string, unknown>).forEach((item) => collectRefs(item, names))
  }
}

/** 将字符串中的 ${name} 引用替换为占位取值；未知引用保持原样。 */
function substitute(s: string, values: Record<string, string>): string {
  return s.replace(/\$\{([a-zA-Z0-9_]+)\}/g, (match, name: string) =>
    name in values ? values[name] : match,
  )
}

/** 递归注入占位取值到预设值。 */
function injectValue(value: unknown, values: Record<string, string>): unknown {
  if (typeof value === 'string') return substitute(value, values)
  if (Array.isArray(value)) return value.map((item) => injectValue(item, values))
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = injectValue(v, values)
    }
    return out
  }
  return value
}

/** 由模板预设参数 + 占位取值构建最终连接参数（含 ${name} 注入 + 顶层补入）。 */
function buildTemplateConnParams(resolved: Record<string, string>): {
  connParams: ConnParams
  credential: string
} {
  const injected = injectValue(presetParams.value, resolved) as ConnParams
  const referenced = new Set<string>()
  collectRefs(presetParams.value, referenced)

  // 未被引用的占位参数按其名补入顶层连接参数（如直接作为 url）。
  for (const ph of placeholders.value) {
    if (!(ph.name in resolved)) continue
    if (!referenced.has(ph.name)) {
      injected[ph.name] = resolved[ph.name]
    }
  }

  // secret 类占位首个值作为凭证。
  let credential = ''
  for (const ph of placeholders.value) {
    if (ph.rule?.kind === 'secret' && ph.name in resolved) {
      credential = resolved[ph.name]
      break
    }
  }
  return { connParams: injected, credential }
}

/** 构建手动模式连接参数（按传输类型）。 */
function buildManualConnParams(): ConnParams {
  if (form.transport === 'stdio') {
    const params: ConnParams = { command: form.command.trim() }
    const args = parseArgs(form.args)
    if (args.length > 0) params.args = args
    return params
  }
  return { url: form.url.trim() }
}

/** 基础名称校验（长度 1-100，Req 2.2）。 */
function validateName(): boolean {
  const n = [...form.name.trim()].length
  if (n < 1 || n > 100) {
    fieldErrors.name = '名称长度必须在 1 至 100 个字符之间'
    return false
  }
  return true
}

/** 构建提交请求体；校验失败返回 null 并填充 fieldErrors。 */
function buildPayload(): UpstreamConfigRequest | null {
  clearErrors()
  let ok = validateName()

  let connParams: ConnParams
  let credential = form.credential

  if (fromTemplate.value) {
    // 模板模式：逐占位校验并注入（Req 14.8-14.11）。
    const resolved: Record<string, string> = {}
    for (const ph of placeholders.value) {
      const raw = placeholderValues[ph.name] ?? ''
      const msg = validatePlaceholder(ph, raw)
      if (msg !== '') {
        fieldErrors[ph.name] = msg
        ok = false
      } else if (raw.trim() !== '') {
        resolved[ph.name] = raw.trim()
      }
    }
    if (!ok) return null
    const built = buildTemplateConnParams(resolved)
    connParams = built.connParams
    credential = built.credential || form.credential
  } else {
    // 手动模式：按传输类型校验必填连接参数（Req 4.5）。
    if (form.transport === 'stdio') {
      if (form.command.trim() === '') {
        fieldErrors.command = '命令（command）为必填项'
        ok = false
      }
    } else {
      const url = form.url.trim()
      const wantWs = form.transport === 'websocket'
      if (url === '') {
        fieldErrors.url = '服务地址（url）为必填项'
        ok = false
      } else if (!isValidURL(url)) {
        fieldErrors.url = '服务地址不是合法 URL'
        ok = false
      } else {
        const proto = url.split(':')[0].toLowerCase()
        const allowed = wantWs ? ['ws', 'wss'] : ['http', 'https']
        if (!allowed.includes(proto)) {
          fieldErrors.url = `服务地址协议必须为 ${allowed.join(' / ')}`
          ok = false
        }
      }
    }
    if (!ok) return null
    connParams = buildManualConnParams()
  }

  return {
    name: form.name.trim(),
    transport: form.transport,
    connParams,
    credential: credential.trim() ? credential.trim() : undefined,
    enabled: form.enabled,
    sortOrder: form.sortOrder,
    autoSync: form.autoSync,
  }
}

/** 从后端错误中提取字段级信息或整体消息。 */
function applyServerError(err: unknown): void {
  // 批 1 后请求层统一抛出 ApiError（含 message/fields）；据此回填字段级错误与整体提示。
  if (err instanceof ApiError) {
    for (const [k, v] of Object.entries(err.fields)) {
      // 后端 connParams.<key> 形式映射回本地字段。
      const local = k.startsWith('connParams.') ? k.slice('connParams.'.length) : k
      fieldErrors[local] = v
    }
    formError.value = err.message || '保存失败，请稍后重试'
    return
  }
  formError.value = err instanceof Error ? err.message : '保存失败，请稍后重试'
}

/** 提交表单（创建或更新）。 */
async function handleSubmit(): Promise<void> {
  if (submitting.value) return
  const payload = buildPayload()
  if (payload === null) return

  submitting.value = true
  try {
    if (isEdit.value && props.upstream !== null) {
      await updateUpstream(props.upstream.id, payload)
    } else {
      await createUpstream(payload)
    }
    emit('saved')
  } catch (err) {
    applyServerError(err)
  } finally {
    submitting.value = false
  }
}

/** 通用输入框样式类（TailAdmin 风格）。 */
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
</script>

<template>
  <!-- 遮罩层 + 右侧抽屉 -->
  <transition name="fade">
    <div
      v-if="open"
      class="fixed inset-0 z-[100000] flex justify-end bg-gray-900/40 backdrop-blur-[1px]"
      @click.self="emit('close')"
    >
      <div
        class="flex h-full w-full max-w-xl flex-col overflow-hidden border-l border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <!-- 抽屉头部 -->
        <div
          class="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <h3 class="text-lg font-semibold text-gray-800 dark:text-white/90">{{ title }}</h3>
          <button
            type="button"
            class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
              <path
                d="M6 6l12 12M6 18L18 6"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>

        <!-- 抽屉表单主体 -->
        <form class="flex-1 overflow-y-auto px-6 py-5" novalidate @submit.prevent="handleSubmit">
          <div class="space-y-5">
            <!-- 名称 -->
            <div>
              <label for="up-name" :class="labelClass">名称<span class="text-error-500">*</span></label>
              <input id="up-name" v-model="form.name" type="text" :class="inputClass" placeholder="上游 MCP 服务名称" />
              <p v-if="fieldErrors.name" class="mt-1 text-xs text-error-500">{{ fieldErrors.name }}</p>
            </div>

            <!-- 传输类型 -->
            <div>
              <label for="up-transport" :class="labelClass">传输类型</label>
              <select
                id="up-transport"
                v-model="form.transport"
                :class="inputClass"
                :disabled="fromTemplate"
              >
                <option v-for="opt in TRANSPORT_OPTIONS" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </option>
              </select>
              <p v-if="fromTemplate" class="mt-1 text-xs text-gray-400">模板已固定传输类型，不可修改</p>
            </div>

            <!-- 模板预设连接参数（只读展示） -->
            <div
              v-if="fromTemplate && Object.keys(presetParams).length > 0"
              class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.03]"
            >
              <p class="mb-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">模板预设连接参数</p>
              <pre class="overflow-x-auto text-xs text-gray-600 dark:text-gray-300">{{ JSON.stringify(presetParams, null, 2) }}</pre>
            </div>

            <!-- 模板占位参数（需用户填写，Req 14.7） -->
            <template v-if="fromTemplate">
              <div v-for="ph in placeholders" :key="ph.name">
                <label :for="`ph-${ph.name}`" :class="labelClass">
                  {{ placeholderLabel(ph) }}
                  <span v-if="ph.required" class="text-error-500">*</span>
                </label>
                <input
                  :id="`ph-${ph.name}`"
                  v-model="placeholderValues[ph.name]"
                  :type="isSecret(ph) ? 'password' : 'text'"
                  :autocomplete="isSecret(ph) ? 'new-password' : 'off'"
                  :class="inputClass"
                  :placeholder="ph.description || `请输入${placeholderLabel(ph)}`"
                />
                <p v-if="ph.description" class="mt-1 text-xs text-gray-400">{{ ph.description }}</p>
                <p v-if="fieldErrors[ph.name]" class="mt-1 text-xs text-error-500">{{ fieldErrors[ph.name] }}</p>
              </div>
            </template>

            <!-- 手动连接参数：stdio -->
            <template v-else-if="form.transport === 'stdio'">
              <div>
                <label for="up-command" :class="labelClass">命令 command<span class="text-error-500">*</span></label>
                <input id="up-command" v-model="form.command" type="text" :class="inputClass" placeholder="如 npx 或可执行文件路径" />
                <p v-if="fieldErrors.command" class="mt-1 text-xs text-error-500">{{ fieldErrors.command }}</p>
              </div>
              <div>
                <label for="up-args" :class="labelClass">参数 args（每行一个或逗号分隔）</label>
                <textarea id="up-args" v-model="form.args" rows="3" :class="[inputClass, 'h-auto py-2.5']" placeholder="-y&#10;@modelcontextprotocol/server-xxx"></textarea>
              </div>
            </template>

            <!-- 手动连接参数：url 类 -->
            <template v-else>
              <div>
                <label for="up-url" :class="labelClass">服务地址 url<span class="text-error-500">*</span></label>
                <input id="up-url" v-model="form.url" type="text" :class="inputClass" :placeholder="form.transport === 'websocket' ? 'wss://example.com/mcp' : 'https://example.com/mcp'" />
                <p v-if="fieldErrors.url" class="mt-1 text-xs text-error-500">{{ fieldErrors.url }}</p>
              </div>
            </template>

            <!-- 凭证（手动模式可选；模板模式由 secret 占位自动填充） -->
            <div v-if="!fromTemplate">
              <label for="up-credential" :class="labelClass">鉴权凭证（可选，不回显）</label>
              <input id="up-credential" v-model="form.credential" type="password" autocomplete="new-password" :class="inputClass" :placeholder="isEdit ? '留空表示不修改' : 'API Key / Token'" />
            </div>

            <!-- 排序顺序 -->
            <div>
              <label for="up-sort" :class="labelClass">排序顺序</label>
              <input id="up-sort" v-model.number="form.sortOrder" type="number" :class="inputClass" />
            </div>

            <!-- 开关组 -->
            <div class="flex flex-wrap gap-6">
              <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                <input v-model="form.enabled" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500/20" />
                启用并参与聚合
              </label>
              <label class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                <input v-model="form.autoSync" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500/20" />
                开启工具列表自动同步
              </label>
            </div>

            <p
              v-if="formError !== ''"
              role="alert"
              class="rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
            >
              {{ formError }}
            </p>
          </div>
        </form>

        <!-- 抽屉底部操作 -->
        <div
          class="flex items-center justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            :disabled="submitting"
            class="rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:cursor-not-allowed disabled:opacity-60"
            @click="handleSubmit"
          >
            {{ submitting ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
