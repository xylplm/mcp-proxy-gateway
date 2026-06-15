<script setup lang="ts">
/**
 * 系统设置页（任务 26.4）。
 *
 * 以 TailAdmin 表单组件风格编辑网关运行参数：同步 cron、连接/重试超时、统计/审计保留期、
 * 会话超时。个人密码、对外服务模式与 API 服务接入已拆到更贴近使用场景的页面。
 *
 * 覆盖 Req 7.3（cron 服务端校验，字段级错误回显）与 17.5（管理 REST API 接入）。
 *
 * 校验策略：字段范围由后端统一强制（见 internal/config/validate.go），前端仅以 min/max
 * 提示与 number 输入辅助，并在保存失败时按后端返回的 fields 将错误定位到对应字段。
 * 响应式：useBreakpoint.isLargeScreen 决定分区内表单为单列（小屏）或双列（大屏）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import FieldLabel from '@/components/common/FieldLabel.vue'
import FloatingActionBar from '@/components/common/FloatingActionBar.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'
import {
  getSettings,
  updateSettings,
  extractAPIError,
  type YAMLConfig,
} from '@/api/settings'

const { isLargeScreen } = useBreakpoint()
const toast = useToast()
const { confirm } = useConfirm()

/** 分区内表单栅格类：大屏两列、小屏单列。 */
const gridClass = computed(() =>
  isLargeScreen.value ? 'grid grid-cols-2 gap-x-6 gap-y-5' : 'grid grid-cols-1 gap-y-5',
)

/** 当前配置表单模型（加载后填充）。 */
const config = ref<YAMLConfig | null>(null)

/** Port helpers: strip ':prefix on load, restore on save. */
const adminPort = ref<number|string>('')
const publicMCPPort = ref<number|string>('')

function addrToPort(addr: string): number|string {
  const s = addr.trim()
  if (s === '') return ''
  const m = s.match(/^(?:[\d.]+|\[?[\da-f:]+\]?)?:(\d+)$/i)
  if (m) return Number(m[1])
  return s
}

function portToAddr(port: number|string): string {
  const n = typeof port === 'number' ? port : Number(port)
  if (!Number.isFinite(n) || n < 1) return ''
  return ':' + String(n)
}

/** 页面级加载/保存状态。 */
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')

/** 保存表单（设置）的整体错误与字段级错误（键为后端字段路径，如 sync.cron）。 */
const formError = ref('')
const fieldErrors = reactive<Record<string, string>>({})

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
    adminPort.value = addrToPort(config.value.server.admin_addr)
    publicMCPPort.value = addrToPort(config.value.server.public_mcp_addr)
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
  const ok = await confirm({
    title: '确认保存系统设置',
    message: '保存后网关会自动重启以应用最新配置。重启期间管理台和对外 MCP 服务会短暂不可用，请确认当前没有关键调用正在进行。',
    confirmText: '保存并重启',
    cancelText: '取消',
    tone: 'warning',
  })
  if (!ok) return

  clearErrors()
  // Restore port strings from numeric port inputs before saving
  config.value.server.admin_addr = portToAddr(adminPort.value)
  config.value.server.public_mcp_addr = portToAddr(publicMCPPort.value)
  saving.value = true
  try {
    config.value = await updateSettings(config.value, { restart: true })
    adminPort.value = addrToPort(config.value.server.admin_addr)
    publicMCPPort.value = addrToPort(config.value.server.public_mcp_addr)
    toast.success('系统设置已保存，网关正在重启')
  } catch (err) {
    applyServerError(err)
  } finally {
    saving.value = false
  }
}

/** 通用样式类（TailAdmin 风格）。 */
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
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
      <p
        v-if="formError !== ''"
        class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ formError }}
      </p>

      <form class="flex flex-col gap-6 pb-28" @submit.prevent="saveSettings">
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
              <FieldLabel label="同步 cron 表达式" required tooltip="用于定期刷新上游 MCP 工具列表，服务端会校验 cron 格式。" />
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
              <FieldLabel label="同步超时（秒）" required tooltip="单个上游工具同步允许等待的最长时间。" />
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
              <FieldLabel label="连接建立超时（秒）" required tooltip="建立上游 MCP 连接时允许等待的最长时间。" />
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
              <FieldLabel label="初始退避（秒）" required tooltip="连接失败后首次重试前等待的时间。" />
              <input
                v-model.number="config.connection.retry_initial_backoff_s"
                type="number"
                min="1"
                max="60"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 60，默认 5。</p>
              <p v-if="fieldErrors['connection.retry_initial_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_initial_backoff_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="退避倍数" required tooltip="连续失败时每次重试等待时间的放大倍数。" />
              <input
                v-model.number="config.connection.retry_multiplier"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需大于等于 1，默认 5。</p>
              <p v-if="fieldErrors['connection.retry_multiplier']" :class="errClass">
                {{ fieldErrors['connection.retry_multiplier'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="退避上限（秒）" required tooltip="自动重试等待时间不会超过这个上限。" />
              <input
                v-model.number="config.connection.retry_max_backoff_s"
                type="number"
                min="1"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 86400，默认 3600。</p>
              <p v-if="fieldErrors['connection.retry_max_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_max_backoff_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="连续失败阈值" required tooltip="达到该失败次数后暂停该上游自动重试。" />
              <input
                v-model.number="config.connection.failure_threshold"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 10；达到阈值后熔断暂停。</p>
              <p v-if="fieldErrors['connection.failure_threshold']" :class="errClass">
                {{ fieldErrors['connection.failure_threshold'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="上游调用超时（秒）" required tooltip="转发工具调用到上游 MCP 时允许等待的最长时间。" />
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

        <!-- 服务监听 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">服务监听</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            管理端口用于后台操作；对外 MCP 可启用独立端口，便于公网暴露时叠加更严格的反向代理和安全策略。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="管理监听端口" required tooltip="管理台与管理 API 的监听端口号。" />
              <input
                v-model.number="adminPort"
                type="number"
                min="1"
                max="65535"
                placeholder="8080"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1–65535，默认 8080。公网部署建议只在内网或反向代理内侧暴露。</p>
              <p v-if="fieldErrors['server.admin_addr']" :class="errClass">
                {{ fieldErrors['server.admin_addr'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="独立 MCP 监听端口" tooltip="对外 MCP 服务的独立监听端口号。留空表示不启用独立端口。" />
              <input
                v-model.number="publicMCPPort"
                type="number"
                min="1"
                max="65535"
                placeholder="8081"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1–65535。留空表示不启用独立端口，启用后可只把该端口暴露到公网。</p>
              <p v-if="fieldErrors['server.public_mcp_addr']" :class="errClass">
                {{ fieldErrors['server.public_mcp_addr'] }}
              </p>
            </div>
          </div>
          <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div class="min-w-0">
                <FieldLabel label="管理端口同时暴露 MCP" tooltip="关闭后，管理监听地址不再注册 /mcp/*；对外客户端只能访问独立 MCP 监听地址。" />
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  启用独立 MCP 监听地址后，建议关闭此项，让管理端口只服务后台；未配置独立端口时不可关闭。
                </p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="config.server.expose_mcp_on_admin_addr"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="config.server.expose_mcp_on_admin_addr ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="config.server.expose_mcp_on_admin_addr = !config.server.expose_mcp_on_admin_addr"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="config.server.expose_mcp_on_admin_addr ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
          </div>
          <div class="mt-5">
            <FieldLabel label="日志级别" required tooltip="控制进程日志输出的详细程度。保存后立即生效，无需重启服务。debug 会记录调用链每次工具调用的入口与结果，便于排查问题但日志量较大。" />
            <select
              v-model="config.server.log_level"
              :class="inputClass"
            >
              <option value="debug">debug（详细，含调用链追踪）</option>
              <option value="info">info（默认，关键事件）</option>
              <option value="warn">warn（仅警告与错误）</option>
              <option value="error">error（仅错误）</option>
            </select>
            <p :class="hintClass">调整后立即生效。排查工具调用问题时建议临时切换到 debug。</p>
            <p v-if="fieldErrors['server.log_level']" :class="errClass">
              {{ fieldErrors['server.log_level'] }}
            </p>
          </div>

          <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <FieldLabel label="外网访问地址" tooltip="公网用户访问本网关的完整地址。容器部署时管理台无法自动探测真实可达域名与端口，填写后对外接口地址示例与对接引导会显示真实外网地址。" />
            <input
              v-model.trim="config.server.public_url"
              type="text"
              placeholder="https://mcp.example.com:8443"
              :class="inputClass"
            />
            <p :class="hintClass">可选。留空则按当前访问域名推断。需为 http/https 完整地址，含协议、域名和端口。</p>
            <p v-if="fieldErrors['server.public_url']" :class="errClass">
              {{ fieldErrors['server.public_url'] }}
            </p>
            <div class="mt-5">
              <FieldLabel label="内网访问地址" tooltip="内网/局域网用户访问本网关的完整地址。填写后对外接口地址与对接引导可切换到内网视图。" />
              <input
                v-model.trim="config.server.lan_url"
                type="text"
                placeholder="http://192.168.1.10:8081"
                :class="inputClass"
              />
              <p :class="hintClass">可选。留空则不显示内网视图。需为 http/https 完整地址，含协议、域名和端口。</p>
              <p v-if="fieldErrors['server.lan_url']" :class="errClass">
                {{ fieldErrors['server.lan_url'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 对外 API 默认值 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">对外 API 默认值</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            对外 MCP 服务在智能模式下使用的默认参数。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="智能模式默认返回工具数" required tooltip="智能模式下，网关工具列表发现接口默认返回的真实工具摘要数量。" />
              <input
                v-model.number="config.mcp_api.smart_discovery_limit"
                type="number"
                min="1"
                max="200"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 200，默认 50。</p>
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
            控制调用记录、统计数据与操作日志的保留时间，避免长期运行后数据无限增长。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="调用记录保留天数" required tooltip="调用记录、调用统计和趋势数据保留时间；后台清理任务会删除超期数据。" />
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
              <FieldLabel label="工具排行默认条数" required tooltip="调用统计页和相关接口未指定条数时使用的默认排行数量。" />
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
              <FieldLabel label="操作日志保留天数" required tooltip="系统操作日志的保留时间，用于审计日志页面和后台清理。" />
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
              <FieldLabel label="审计分页默认每页条数" required tooltip="审计日志接口未指定页大小时使用的默认数量。" />
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
              <FieldLabel label="会话超时（秒）" required tooltip="管理后台登录会话的有效时间，超时后需要重新登录。" />
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

        <FloatingActionBar>
          <template #info>
            <p class="text-sm font-medium text-gray-800 dark:text-white/90">系统设置</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">修改后保存才会写入；服务监听配置需重启后生效。</p>
          </template>
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
        </FloatingActionBar>
      </form>

    </template>
  </AdminLayout>
</template>
