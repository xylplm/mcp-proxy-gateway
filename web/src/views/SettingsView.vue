<script setup lang="ts">
/**
 * 系统设置页（任务 26.4）。
 *
 * 以 TailAdmin 表单组件风格（分区卡片、标签化输入、下拉、开关、数值输入）编辑常规配置：
 * 同步 cron 与同步超时、连接/重试各超时、对外 MCP 模式、统计/审计保留期、会话超时、
 * 小智接入（Req 15.1），以及独立的管理员改密表单（Req 1.8）。
 *
 * 覆盖 Req 7.3（cron 服务端校验，字段级错误回显）、15.1（小智接入开关与接入点）、
 * 17.5（管理 REST API 接入）。
 *
 * 校验策略：字段范围由后端统一强制（见 internal/config/validate.go），前端仅以 min/max
 * 提示与 number 输入辅助，并在保存失败时按后端返回的 fields 将错误定位到对应字段。
 * 响应式：useBreakpoint.isLargeScreen 决定分区内表单为单列（小屏）或双列（大屏）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import {
  getSettings,
  updateSettings,
  changePassword,
  extractAPIError,
  type YAMLConfig,
  type MCPMode,
} from '@/api/settings'

const { isLargeScreen } = useBreakpoint()

/** 分区内表单栅格类：大屏两列、小屏单列。 */
const gridClass = computed(() =>
  isLargeScreen.value ? 'grid grid-cols-2 gap-x-6 gap-y-5' : 'grid grid-cols-1 gap-y-5',
)

/** 当前配置表单模型（加载后填充）。 */
const config = ref<YAMLConfig | null>(null)

/** 页面级加载/保存状态。 */
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')

/** 保存表单（设置）的整体错误与字段级错误（键为后端字段路径，如 sync.cron）。 */
const formError = ref('')
const fieldErrors = reactive<Record<string, string>>({})

/** 成功提示。 */
const toast = ref('')

/** 对外模式可选项。 */
const MODE_OPTIONS: ReadonlyArray<{ value: MCPMode; label: string }> = [
  { value: 'smart', label: '智能模式（smart，仅暴露少量网关工具）' },
  { value: 'full', label: '全量模式（full，一次性暴露全部聚合工具）' },
]

/** 展示短暂成功提示。 */
function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

/** 清空所有字段级错误与整体错误。 */
function clearErrors(): void {
  formError.value = ''
  for (const k of Object.keys(fieldErrors)) {
    delete fieldErrors[k]
  }
}

/** 加载当前配置快照。 */
async function loadSettings(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    config.value = await getSettings()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载系统设置失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadSettings)

/** 将后端返回的字段级错误映射到本地，并设置整体错误信息。 */
function applyServerError(err: unknown): void {
  const body = extractAPIError(err)
  if (body?.fields) {
    for (const [k, v] of Object.entries(body.fields)) {
      fieldErrors[k] = v
    }
  }
  formError.value =
    body?.message ?? (err instanceof Error ? err.message : '保存失败，请稍后重试')
}

/** 保存常规配置（Req 7.3、18.4）。 */
async function saveSettings(): Promise<void> {
  if (config.value === null || saving.value) return
  clearErrors()
  saving.value = true
  try {
    config.value = await updateSettings(config.value)
    showToast('系统设置已保存')
  } catch (err) {
    applyServerError(err)
  } finally {
    saving.value = false
  }
}

// ── 改密表单（独立提交，调用 change-password 端点）────────────────────────
const pwd = reactive({ currentPassword: '', newPassword: '', confirmPassword: '' })
const pwdSaving = ref(false)
const pwdError = ref('')
const pwdFieldErrors = reactive<Record<string, string>>({})

function clearPwdErrors(): void {
  pwdError.value = ''
  for (const k of Object.keys(pwdFieldErrors)) {
    delete pwdFieldErrors[k]
  }
}

/** 提交改密（Req 1.8、1.10）。 */
async function submitChangePassword(): Promise<void> {
  if (pwdSaving.value) return
  clearPwdErrors()

  // 前端轻量校验：新密码长度 6-128，且与确认一致。
  if (pwd.newPassword.length < 6 || pwd.newPassword.length > 128) {
    pwdFieldErrors.newPassword = '新密码长度需在 6 至 128 个字符之间'
    return
  }
  if (pwd.newPassword !== pwd.confirmPassword) {
    pwdFieldErrors.confirmPassword = '两次输入的新密码不一致'
    return
  }

  pwdSaving.value = true
  try {
    await changePassword({
      currentPassword: pwd.currentPassword,
      newPassword: pwd.newPassword,
    })
    pwd.currentPassword = ''
    pwd.newPassword = ''
    pwd.confirmPassword = ''
    showToast('管理员密码已更新')
  } catch (err) {
    const body = extractAPIError(err)
    if (body?.fields) {
      for (const [k, v] of Object.entries(body.fields)) {
        // 后端字段名归一为本地键（newPassword/currentPassword）。
        pwdFieldErrors[k] = v
      }
    }
    pwdError.value =
      body?.message ?? (err instanceof Error ? err.message : '改密失败，请稍后重试')
  } finally {
    pwdSaving.value = false
  }
}

/** 通用样式类（TailAdmin 风格）。 */
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
const hintClass = 'mt-1 text-xs text-gray-400 dark:text-gray-500'
const errClass = 'mt-1 text-xs text-error-500'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="系统设置" />

    <!-- 加载/错误态 -->
    <div
      v-if="loading"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      加载中…
    </div>
    <p
      v-else-if="loadError !== ''"
      class="rounded-2xl border border-error-200 bg-error-50 px-5 py-4 text-sm text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ loadError }}
      <button type="button" class="ml-2 underline" @click="loadSettings">重试</button>
    </p>

    <template v-else-if="config !== null">
      <!-- 全局提示 -->
      <p
        v-if="toast !== ''"
        class="mb-4 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
      >
        {{ toast }}
      </p>
      <p
        v-if="formError !== ''"
        class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ formError }}
      </p>

      <form class="flex flex-col gap-6" @submit.prevent="saveSettings">
        <!-- 同步 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">同步</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            工具列表自动同步的调度与超时设置（Req 7）。
          </p>
          <div :class="gridClass">
            <div>
              <label :class="labelClass">同步 cron 表达式</label>
              <input
                v-model="config.sync.cron"
                type="text"
                placeholder="如：0 */30 * * * *"
                :class="inputClass"
              />
              <p :class="hintClass">标准 6 段 cron 表达式，由服务端校验合法性。</p>
              <p v-if="fieldErrors['sync.cron']" :class="errClass">{{ fieldErrors['sync.cron'] }}</p>
            </div>
            <div>
              <label :class="labelClass">同步超时（秒）</label>
              <input
                v-model.number="config.sync.timeout_s"
                type="number"
                min="5"
                max="300"
                :class="inputClass"
              />
              <p :class="hintClass">范围 5 – 300，默认 30。</p>
              <p v-if="fieldErrors['sync.timeout_s']" :class="errClass">
                {{ fieldErrors['sync.timeout_s'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 超时与重试 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">超时与重试</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            上游连接、重试退避与聚合调用的超时设置（Req 4、5、10）。
          </p>
          <div :class="gridClass">
            <div>
              <label :class="labelClass">连接建立超时（秒）</label>
              <input
                v-model.number="config.connection.connect_timeout_s"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需为正整数，默认 30。</p>
              <p v-if="fieldErrors['connection.connect_timeout_s']" :class="errClass">
                {{ fieldErrors['connection.connect_timeout_s'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">初始退避（秒）</label>
              <input
                v-model.number="config.connection.retry_initial_backoff_s"
                type="number"
                min="1"
                max="60"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 60，默认 1。</p>
              <p v-if="fieldErrors['connection.retry_initial_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_initial_backoff_s'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">退避倍数</label>
              <input
                v-model.number="config.connection.retry_multiplier"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需大于等于 1，默认 2。</p>
              <p v-if="fieldErrors['connection.retry_multiplier']" :class="errClass">
                {{ fieldErrors['connection.retry_multiplier'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">退避上限（秒）</label>
              <input
                v-model.number="config.connection.retry_max_backoff_s"
                type="number"
                min="1"
                max="3600"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 3600，默认 60。</p>
              <p v-if="fieldErrors['connection.retry_max_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_max_backoff_s'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">连续失败阈值</label>
              <input
                v-model.number="config.connection.failure_threshold"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 5；达到阈值后熔断暂停。</p>
              <p v-if="fieldErrors['connection.failure_threshold']" :class="errClass">
                {{ fieldErrors['connection.failure_threshold'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">上游调用超时（秒）</label>
              <input
                v-model.number="config.aggregation.upstream_call_timeout_s"
                type="number"
                min="1"
                max="600"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 600，默认 30。</p>
              <p v-if="fieldErrors['aggregation.upstream_call_timeout_s']" :class="errClass">
                {{ fieldErrors['aggregation.upstream_call_timeout_s'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 对外 MCP 模式 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">
            对外 MCP 模式
          </h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            对外暴露聚合工具的模式与智能模式发现上限（Req 11）。
          </p>
          <div :class="gridClass">
            <div>
              <label :class="labelClass">对外模式</label>
              <select v-model="config.mcp_api.mode" :class="inputClass">
                <option v-for="opt in MODE_OPTIONS" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </option>
              </select>
              <p v-if="fieldErrors['mcp_api.mode']" :class="errClass">
                {{ fieldErrors['mcp_api.mode'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">智能模式发现上限</label>
              <input
                v-model.number="config.mcp_api.smart_discovery_limit"
                type="number"
                min="1"
                max="200"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 200，默认 50；仅智能模式生效。</p>
              <p v-if="fieldErrors['mcp_api.smart_discovery_limit']" :class="errClass">
                {{ fieldErrors['mcp_api.smart_discovery_limit'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 保留期 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">保留期</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            统计与审计日志的默认条数与数据保留天数（Req 16、22）。
          </p>
          <div :class="gridClass">
            <div>
              <label :class="labelClass">统计保留天数</label>
              <input
                v-model.number="config.statistics.retention_days"
                type="number"
                min="1"
                max="3650"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 3650，默认 90。</p>
              <p v-if="fieldErrors['statistics.retention_days']" :class="errClass">
                {{ fieldErrors['statistics.retention_days'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">工具排行默认条数</label>
              <input
                v-model.number="config.statistics.top_limit_default"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 10。</p>
              <p v-if="fieldErrors['statistics.top_limit_default']" :class="errClass">
                {{ fieldErrors['statistics.top_limit_default'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">审计保留天数</label>
              <input
                v-model.number="config.audit.retention_days"
                type="number"
                min="1"
                max="3650"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 3650，默认 180。</p>
              <p v-if="fieldErrors['audit.retention_days']" :class="errClass">
                {{ fieldErrors['audit.retention_days'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">审计分页默认每页条数</label>
              <input
                v-model.number="config.audit.page_size_default"
                type="number"
                min="1"
                max="200"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 200，默认 20。</p>
              <p v-if="fieldErrors['audit.page_size_default']" :class="errClass">
                {{ fieldErrors['audit.page_size_default'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 会话 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">会话</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            管理后台会话令牌的有效期（Req 1.4、1.7）。
          </p>
          <div :class="gridClass">
            <div>
              <label :class="labelClass">会话超时（秒）</label>
              <input
                v-model.number="config.auth.session_timeout_s"
                type="number"
                min="300"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">范围 300 – 86400，默认 3600。</p>
              <p v-if="fieldErrors['auth.session_timeout_s']" :class="errClass">
                {{ fieldErrors['auth.session_timeout_s'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 小智接入 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">小智接入</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            启用后通过 WebSocket 接入小智 MCP 端点（Req 15.1、15.6）。
          </p>
          <div class="flex flex-col gap-5">
            <div class="flex items-center justify-between">
              <div>
                <label :class="labelClass">启用小智接入</label>
                <p class="text-xs text-gray-400 dark:text-gray-500">
                  开启后须填写合法的 ws:// 或 wss:// 接入点地址。
                </p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="config.xiaozhi.enabled"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="config.xiaozhi.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="config.xiaozhi.enabled = !config.xiaozhi.enabled"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="config.xiaozhi.enabled ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
            <div>
              <label :class="labelClass">接入点地址</label>
              <input
                v-model="config.xiaozhi.endpoint"
                type="text"
                placeholder="如：wss://xiaozhi.example.com/mcp"
                :disabled="!config.xiaozhi.enabled"
                :class="[inputClass, !config.xiaozhi.enabled ? 'opacity-60' : '']"
              />
              <p :class="hintClass">协议须为 ws:// 或 wss://，且包含主机名。</p>
              <p v-if="fieldErrors['xiaozhi.endpoint']" :class="errClass">
                {{ fieldErrors['xiaozhi.endpoint'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 保存栏 -->
        <div class="flex items-center justify-end gap-3">
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="saving"
            @click="loadSettings"
          >
            重置
          </button>
          <button
            type="submit"
            class="rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
            :disabled="saving"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </div>
      </form>

      <!-- 改密（独立提交，调用 change-password 端点）-->
      <section
        class="mt-6 rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">修改密码</h3>
        <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
          校验当前密码后更新管理员密码；与系统设置分别提交（Req 1.8、1.10）。
        </p>

        <p
          v-if="pwdError !== ''"
          class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ pwdError }}
        </p>

        <form class="flex flex-col gap-5" @submit.prevent="submitChangePassword">
          <div :class="gridClass">
            <div>
              <label :class="labelClass">当前密码</label>
              <input
                v-model="pwd.currentPassword"
                type="password"
                autocomplete="current-password"
                :class="inputClass"
              />
              <p v-if="pwdFieldErrors['currentPassword']" :class="errClass">
                {{ pwdFieldErrors['currentPassword'] }}
              </p>
            </div>
            <div></div>
            <div>
              <label :class="labelClass">新密码</label>
              <input
                v-model="pwd.newPassword"
                type="password"
                autocomplete="new-password"
                :class="inputClass"
              />
              <p :class="hintClass">长度 8 – 128 个字符。</p>
              <p v-if="pwdFieldErrors['newPassword']" :class="errClass">
                {{ pwdFieldErrors['newPassword'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">确认新密码</label>
              <input
                v-model="pwd.confirmPassword"
                type="password"
                autocomplete="new-password"
                :class="inputClass"
              />
              <p v-if="pwdFieldErrors['confirmPassword']" :class="errClass">
                {{ pwdFieldErrors['confirmPassword'] }}
              </p>
            </div>
          </div>
          <div class="flex justify-end">
            <button
              type="submit"
              class="rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
              :disabled="pwdSaving"
            >
              {{ pwdSaving ? '提交中…' : '修改密码' }}
            </button>
          </div>
        </form>
      </section>
    </template>
  </AdminLayout>
</template>
