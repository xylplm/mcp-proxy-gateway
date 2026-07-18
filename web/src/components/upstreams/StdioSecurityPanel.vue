<script setup lang="ts">
import PathField from '@/components/common/PathField.vue'
import {
  STDIO_SECURITY_MODES,
  securityModeBadgeClass,
  securityModeCardClass,
  type NetworkAccessMode,
  type StdioSecurityMode,
} from '@/utils/stdioSecurity'

withDefaults(
  defineProps<{
    securityMode: StdioSecurityMode
    unrestrictedAck: boolean
    securityNote: string
    filePathsText: string
    packageAllowlistText: string
    networkMode: NetworkAccessMode
    networkHostsText: string
    allowSelfInstall: boolean
    cwd: string
    pathPickerContextRoots?: string[]
    compact?: boolean
    fieldErrors?: Record<string, string>
    securityError?: string
    inputClass?: string
    textareaClass?: string
    labelClass?: string
    helpClass?: string
    errorClass?: string
  }>(),
  {
    pathPickerContextRoots: () => [],
    compact: false,
    fieldErrors: () => ({}),
    securityError: '',
    inputClass:
      'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30',
    textareaClass:
      'min-h-[108px] w-full rounded-lg border border-gray-300 bg-transparent px-4 py-2.5 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30',
    labelClass: 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300',
    helpClass: 'mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400',
    errorClass: 'mt-1.5 text-xs text-error-500',
  },
)

const emit = defineEmits<{
  'update:securityMode': [value: StdioSecurityMode]
  'update:unrestrictedAck': [value: boolean]
  'update:securityNote': [value: string]
  'update:filePathsText': [value: string]
  'update:packageAllowlistText': [value: string]
  'update:networkMode': [value: NetworkAccessMode]
  'update:networkHostsText': [value: string]
  'update:allowSelfInstall': [value: boolean]
  'update:cwd': [value: string]
  'set-mode': [value: StdioSecurityMode]
}>()

const securityModes = STDIO_SECURITY_MODES

function pickMode(mode: StdioSecurityMode): void {
  emit('set-mode', mode)
  emit('update:securityMode', mode)
}

function modeButtonClass(active: boolean, mode: StdioSecurityMode): string {
  if (!active) {
    return 'border-gray-200 bg-white/70 text-gray-600 hover:border-gray-300 dark:border-gray-700 dark:bg-gray-900/40 dark:text-gray-300'
  }
  if (mode === 'unrestricted') return 'border-error-500 bg-error-600 text-white shadow-sm'
  if (mode === 'strict') {
    return 'border-success-400 bg-success-50 text-success-900 dark:border-success-500/50 dark:bg-success-500/15 dark:text-success-200'
  }
  return 'border-brand-300 text-brand-900 dark:border-brand-500/40 dark:text-brand-100 bg-white shadow-sm dark:bg-gray-900'
}
</script>

<template>
  <div class="rounded-xl border p-4 transition" :class="securityModeCardClass(securityMode)">
    <div class="flex flex-wrap items-start justify-between gap-2">
      <div>
        <h5 class="text-sm font-semibold text-gray-900 dark:text-white/90">本地运行安全档位</h5>
        <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-400">
          <template v-if="compact">
            模板创建同样受档位约束。完全放行需勾选确认并填写备注。
          </template>
          <template v-else>
            标准兼容常用 MCP；严格收敛命令/文件/自装包，Linux 有 bubblewrap 时启用文件 bind 与网络
            deny 命名空间隔离；完全放行与网关同权限。
          </template>
        </p>
      </div>
      <span
        class="inline-flex rounded-full px-2.5 py-1 text-[11px] font-semibold"
        :class="securityModeBadgeClass(securityMode)"
      >
        {{ securityModes.find((m) => m.value === securityMode)?.label || securityMode }}
      </span>
    </div>

    <div class="mt-3 grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="本地运行安全档位">
      <button
        v-for="m in securityModes"
        :key="m.value"
        type="button"
        role="radio"
        :aria-checked="securityMode === m.value"
        class="rounded-lg border px-3 py-2.5 text-left transition"
        :class="modeButtonClass(securityMode === m.value, m.value)"
        @click="pickMode(m.value)"
      >
        <div class="text-xs font-semibold">{{ m.label }}</div>
        <div
          v-if="!compact"
          class="mt-1 text-[11px] leading-4 opacity-90"
          :class="
            securityMode === m.value && m.value === 'unrestricted'
              ? 'text-white/90'
              : 'text-gray-500 dark:text-gray-400'
          "
        >
          {{ m.desc }}
        </div>
      </button>
    </div>

    <div
      v-if="securityMode === 'unrestricted'"
      class="border-error-400/80 bg-error-600 mt-3 rounded-xl border px-3.5 py-3 text-xs leading-5 text-white shadow-sm"
    >
      <p v-if="!compact" class="font-semibold tracking-wide">完全放行 · 极高风险</p>
      <p v-if="!compact" class="mt-1 text-white/90">
        子进程与网关同用户权限，可能读写其可见文件并发起网络请求。恶意或被篡改的本地 MCP
        等同于在本机执行任意代码。
      </p>
      <button
        type="button"
        role="checkbox"
        class="group mt-2.5 flex w-full items-start gap-2.5 rounded-lg text-left transition"
        :aria-checked="unrestrictedAck"
        @click="emit('update:unrestrictedAck', !unrestrictedAck)"
      >
        <span
          class="mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-md border transition duration-150"
          :class="
            unrestrictedAck
              ? 'text-error-600 border-white bg-white shadow-sm'
              : 'border-white/50 bg-white/10 text-transparent group-hover:border-white/80 group-hover:bg-white/15'
          "
          aria-hidden="true"
        >
          <svg class="h-3.5 w-3.5" viewBox="0 0 16 16" fill="none">
            <path
              d="M3.5 8.2 6.4 11l6.1-6.5"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </span>
        <span class="min-w-0 leading-5">
          <span class="block font-medium">我已了解风险，仍要为此上游启用完全放行</span>
          <span class="mt-0.5 block text-[11px] leading-4 text-white/80">
            勾选表示接受与网关同权限运行本地 MCP；建议填写放行原因
          </span>
        </span>
      </button>
      <input
        :value="securityNote"
        type="text"
        class="border-error-300/60 mt-2.5 w-full rounded-lg border bg-white/10 px-2.5 py-2 text-xs text-white placeholder:text-white/60 focus:border-white/80 focus:outline-none"
        :placeholder="
          compact
            ? '确认备注（推荐填写原因；未填将写入默认确认语）'
            : '可选：放行原因（写入配置备注）'
        "
        maxlength="300"
        @input="emit('update:securityNote', ($event.target as HTMLInputElement).value)"
      />
    </div>

    <div v-if="compact && securityMode === 'strict'" class="mt-3 space-y-2">
      <label for="tpl-cwd-sec" :class="labelClass"
        >工作目录<span class="text-error-500">*</span></label
      >
      <PathField
        input-id="tpl-cwd-sec"
        :model-value="cwd"
        mode="directory"
        title="选择工作目录（网关主机）"
        :input-class="inputClass"
        :context-roots="pathPickerContextRoots"
        @update:model-value="emit('update:cwd', $event)"
      />
      <p v-if="fieldErrors.cwd" :class="errorClass">{{ fieldErrors.cwd }}</p>
      <label for="tpl-file-roots" :class="labelClass"
        >文件允许路径<span class="text-error-500">*</span></label
      >
      <PathField
        input-id="tpl-file-roots"
        :model-value="filePathsText"
        mode="directory"
        multiple
        :rows="2"
        title="添加文件允许路径（网关主机）"
        :input-class="textareaClass"
        :context-roots="pathPickerContextRoots"
        @update:model-value="emit('update:filePathsText', $event)"
      />
      <p v-if="fieldErrors.filePathsText" :class="errorClass">{{ fieldErrors.filePathsText }}</p>
    </div>

    <div
      v-if="!compact && (securityMode === 'strict' || filePathsText.trim() !== '')"
      class="mt-3 space-y-3"
    >
      <div>
        <label for="up-file-roots" :class="labelClass">
          文件允许路径
          <span v-if="securityMode === 'strict'" class="text-error-500">*</span>
        </label>
        <PathField
          input-id="up-file-roots"
          :model-value="filePathsText"
          mode="directory"
          multiple
          :rows="3"
          title="添加文件允许路径（网关主机）"
          placeholder="每行一个绝对路径，例如：&#10;D:\\mcp-workspace&#10;/data/workspaces/demo"
          :input-class="textareaClass"
          :context-roots="pathPickerContextRoots"
          @update:model-value="emit('update:filePathsText', $event)"
        />
        <p :class="helpClass">
          严格模式要求工作目录位于这些路径内。Linux 有 bubblewrap 时会把这些路径 bind
          进沙箱；其余平台为策略校验。
        </p>
        <p v-if="fieldErrors.filePathsText" :class="errorClass">{{ fieldErrors.filePathsText }}</p>
      </div>
      <div v-if="securityMode === 'strict'">
        <label for="up-pkg-allow" :class="labelClass">追加包白名单（npx / uvx）</label>
        <textarea
          id="up-pkg-allow"
          :value="packageAllowlistText"
          rows="3"
          :class="textareaClass"
          placeholder="每行一个包名，将与系统全局包白名单合并：&#10;@my-org/*&#10;my-custom-mcp"
          @input="emit('update:packageAllowlistText', ($event.target as HTMLTextAreaElement).value)"
        ></textarea>
        <p :class="helpClass">
          严格模式允许使用 npx/uvx，但目标包必须在全局或此处追加的白名单内。支持
          @scope/*。禁止本地路径与 URL。
        </p>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label for="up-net-mode" :class="labelClass">网络策略</label>
          <select
            id="up-net-mode"
            :value="networkMode"
            :class="inputClass"
            @change="
              emit('update:networkMode', ($event.target as HTMLSelectElement).value as NetworkAccessMode)
            "
          >
            <option value="inherit">跟随档位默认</option>
            <option value="deny">拒绝出站（Linux 真断网）</option>
            <option value="allowlist">仅允许声明主机（策略声明）</option>
            <option value="unrestricted">不限制</option>
          </select>
        </div>
        <div>
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
            <input
              :checked="allowSelfInstall"
              type="checkbox"
              class="text-brand-600 focus:ring-brand-500 h-4 w-4 rounded border-gray-300"
              @change="
                emit('update:allowSelfInstall', ($event.target as HTMLInputElement).checked)
              "
            />
            允许脚本自装包（npm/pip 等）
          </label>
          <p :class="helpClass">严格模式默认关闭；开启后需声明网络允许主机。</p>
        </div>
      </div>
      <div v-if="networkMode === 'allowlist'">
        <label for="up-net-hosts" :class="labelClass">网络允许主机</label>
        <textarea
          id="up-net-hosts"
          :value="networkHostsText"
          rows="2"
          :class="textareaClass"
          placeholder="registry.npmjs.org&#10;pypi.org"
          @input="emit('update:networkHostsText', ($event.target as HTMLTextAreaElement).value)"
        ></textarea>
        <p :class="helpClass">
          主机 allowlist 为策略与预检声明；无特权环境无法内核级按域名过滤。需要真断网请选「拒绝出站」。
        </p>
      </div>
    </div>

    <p v-if="fieldErrors.securityProfile" :class="errorClass">{{ fieldErrors.securityProfile }}</p>
    <p v-if="securityError" class="text-error-600 dark:text-error-400 mt-2 text-xs">
      {{ securityError }}
    </p>
  </div>
</template>
