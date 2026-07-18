<script setup lang="ts">
import PathField from '@/components/common/PathField.vue'
import type { DirectoryLaunchEntry } from '@/api/runtime'
import type { ScriptItem } from '@/api/scripts'
import {
  languageLabel as scriptLanguageLabel,
  riskBadgeClass as scriptRiskBadgeClass,
  riskLabel as scriptRiskLabel,
} from '@/utils/scripts'

export type StdioLaunchMode = 'command' | 'script' | 'directory'

withDefaults(
  defineProps<{
    launchMode: StdioLaunchMode
    scriptId: string
    scriptVersion: string
    managedScripts: ScriptItem[]
    scriptsLoading?: boolean
    scriptBindingLoading?: boolean
    directoryRoot: string
    directoryEntryId: string
    directoryEntries: DirectoryLaunchEntry[]
    directoryWarnings?: string[]
    directoryInspectLoading?: boolean
    command: string
    args: string
    commandPlaceholder?: string
    pathPickerContextRoots?: string[]
    fieldErrors?: Record<string, string>
    inputClass?: string
    textareaClass?: string
    labelClass?: string
    helpClass?: string
    errorClass?: string
  }>(),
  {
    scriptsLoading: false,
    scriptBindingLoading: false,
    directoryWarnings: () => [],
    directoryInspectLoading: false,
    commandPlaceholder: 'node / path/to/server.js',
    pathPickerContextRoots: () => [],
    fieldErrors: () => ({}),
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
  'update:launchMode': [value: StdioLaunchMode]
  'update:command': [value: string]
  'update:args': [value: string]
  'update:directoryRoot': [value: string]
  'select-script-mode': []
  'apply-script': [scriptId: string]
  'refresh-scripts': []
  'inspect-directory': []
  'apply-directory-entry': [entry: DirectoryLaunchEntry]
}>()

function modeCardClass(active: boolean, tone: 'brand' | 'success' | 'warning'): string {
  if (!active) {
    return 'hover:border-brand-200 dark:hover:border-brand-500/30 border-gray-200 dark:border-gray-800'
  }
  if (tone === 'success') {
    return 'border-success-300 bg-success-50/70 ring-success-100 dark:border-success-500/40 dark:bg-success-500/10 ring-1'
  }
  if (tone === 'warning') {
    return 'border-warning-300 bg-warning-50/70 ring-warning-100 dark:border-warning-500/40 dark:bg-warning-500/10 ring-1'
  }
  return 'border-brand-300 bg-brand-50/70 ring-brand-100 dark:border-brand-500/40 dark:bg-brand-500/10 ring-1'
}

function selectedScript(scripts: ScriptItem[], id: string): ScriptItem | undefined {
  return scripts.find((item) => item.id === id)
}
</script>

<template>
  <div class="space-y-4">
    <div>
      <span :class="labelClass">本地启动方式</span>
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <button
          type="button"
          class="rounded-xl border p-3 text-left transition"
          :class="modeCardClass(launchMode === 'command', 'brand')"
          @click="emit('update:launchMode', 'command')"
        >
          <span class="block text-sm font-semibold text-gray-800 dark:text-white/90">命令启动</span>
          <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400"
            >手动填写白名单命令、参数与工作目录。</span
          >
        </button>
        <button
          type="button"
          class="rounded-xl border p-3 text-left transition"
          :class="modeCardClass(launchMode === 'script', 'success')"
          @click="emit('select-script-mode')"
        >
          <span class="block text-sm font-semibold text-gray-800 dark:text-white/90"
            >脚本中心启动</span
          >
          <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400"
            >选择受管脚本，自动填入解释器、版本路径与工作目录。</span
          >
        </button>
        <button
          type="button"
          class="rounded-xl border p-3 text-left transition"
          :class="modeCardClass(launchMode === 'directory', 'warning')"
          @click="emit('update:launchMode', 'directory')"
        >
          <span class="block text-sm font-semibold text-gray-800 dark:text-white/90"
            >本地目录启动</span
          >
          <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400"
            >选择项目目录，只读识别清单或常见入口后生成启动配置。</span
          >
        </button>
      </div>
    </div>

    <div
      v-if="launchMode === 'script'"
      class="border-success-200 bg-success-50/40 dark:border-success-500/20 dark:bg-success-500/5 rounded-xl border p-4"
    >
      <div class="flex flex-wrap items-start justify-between gap-2">
        <div>
          <label for="up-script" :class="labelClass"
            >受管脚本<span class="text-error-500">*</span></label
          >
          <p class="-mt-1 text-xs text-gray-500 dark:text-gray-400">
            脚本来自「脚本中心」，保存为不可变版本并经过静态风险分析。
          </p>
        </div>
        <router-link
          to="/scripts"
          class="text-brand-600 dark:text-brand-400 text-xs font-medium hover:underline"
          >打开脚本中心</router-link
        >
      </div>
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
        <select
          id="up-script"
          :value="scriptId"
          :class="[inputClass, 'sm:flex-1']"
          :disabled="scriptsLoading || scriptBindingLoading"
          @change="emit('apply-script', ($event.target as HTMLSelectElement).value)"
        >
          <option value="">
            {{ scriptsLoading ? '加载脚本中…' : '请选择脚本' }}
          </option>
          <option v-for="script in managedScripts" :key="script.id" :value="script.id">
            {{ script.name }} · {{ scriptLanguageLabel(script.language) }} ·
            {{ script.currentVersion }} · {{ scriptRiskLabel(script.risk.level) }}风险
          </option>
        </select>
        <button
          type="button"
          class="h-11 shrink-0 rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-600 transition hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/5"
          :disabled="scriptsLoading || scriptBindingLoading"
          @click="emit('refresh-scripts')"
        >
          {{ scriptsLoading ? '刷新中…' : '刷新列表' }}
        </button>
      </div>
      <div v-if="scriptId" class="mt-3 rounded-lg bg-white/80 p-3 dark:bg-gray-900/50">
        <template v-if="selectedScript(managedScripts, scriptId)">
          <div class="flex flex-wrap items-center gap-2 text-xs">
            <span class="font-medium text-gray-700 dark:text-gray-200">{{
              selectedScript(managedScripts, scriptId)?.name
            }}</span>
            <span
              class="rounded-full px-2 py-0.5 font-semibold"
              :class="
                scriptRiskBadgeClass(selectedScript(managedScripts, scriptId)?.risk.level ?? 'low')
              "
            >
              {{ scriptRiskLabel(selectedScript(managedScripts, scriptId)?.risk.level ?? 'low') }}风险
              · {{ selectedScript(managedScripts, scriptId)?.risk.score ?? 0 }}
            </span>
            <span class="text-gray-400">版本 {{ scriptVersion }}</span>
          </div>
          <p class="mt-2 truncate font-mono text-[11px] text-gray-500 dark:text-gray-400">
            {{ selectedScript(managedScripts, scriptId)?.entryPath }}
          </p>
        </template>
      </div>
      <p v-if="fieldErrors.scriptId" :class="errorClass">{{ fieldErrors.scriptId }}</p>
    </div>

    <div
      v-if="launchMode === 'directory'"
      class="border-warning-200 bg-warning-50/40 dark:border-warning-500/20 dark:bg-warning-500/5 rounded-xl border p-4"
    >
      <label for="up-directory-root" :class="labelClass"
        >项目目录<span class="text-error-500">*</span></label
      >
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
        <PathField
          input-id="up-directory-root"
          :model-value="directoryRoot"
          mode="directory"
          title="选择本地 MCP 项目目录（网关主机）"
          placeholder="/data/workspaces/my-mcp"
          :input-class="inputClass"
          :context-roots="pathPickerContextRoots"
          @update:model-value="emit('update:directoryRoot', $event)"
        />
        <button
          type="button"
          class="bg-warning-500 hover:bg-warning-600 rounded-lg px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          :disabled="!directoryRoot.trim() || directoryInspectLoading"
          @click="emit('inspect-directory')"
        >
          {{ directoryInspectLoading ? '识别中…' : '识别入口' }}
        </button>
      </div>
      <p :class="helpClass">
        优先读取 mpg.launch.json；没有清单时识别
        main.py、server.py、index.js、dist/index.js。只读扫描，不执行代码。
      </p>
      <p v-if="fieldErrors.directoryRoot" :class="errorClass">{{ fieldErrors.directoryRoot }}</p>
      <div v-if="directoryEntries.length > 0" class="mt-3 space-y-2">
        <button
          v-for="entry in directoryEntries"
          :key="entry.id"
          type="button"
          class="flex w-full items-start justify-between gap-3 rounded-lg border p-3 text-left transition"
          :class="
            directoryEntryId === entry.id
              ? 'border-warning-400 ring-warning-200 dark:ring-warning-500/30 bg-white ring-1 dark:bg-gray-900'
              : 'border-warning-200 hover:border-warning-300 dark:border-warning-500/20 bg-white/60 dark:bg-white/5'
          "
          @click="emit('apply-directory-entry', entry)"
        >
          <span class="min-w-0">
            <span class="block text-sm font-medium text-gray-800 dark:text-white/90">{{
              entry.label || entry.id
            }}</span>
            <span class="mt-1 block truncate font-mono text-[11px] text-gray-500"
              >{{ entry.command }} {{ entry.args.join(' ') }}</span
            >
          </span>
          <span class="shrink-0 text-[10px] text-gray-400">{{
            entry.recommendedMode || 'strict'
          }}</span>
        </button>
      </div>
      <p
        v-if="directoryWarnings.length > 0"
        class="text-warning-700 dark:text-warning-300 mt-2 text-xs"
      >
        {{ directoryWarnings.join('；') }}
      </p>
      <p v-if="fieldErrors.directoryEntryId" :class="errorClass">
        {{ fieldErrors.directoryEntryId }}
      </p>
    </div>

    <div>
      <label for="up-command" :class="labelClass"
        >启动命令<span class="text-error-500">*</span></label
      >
      <input
        id="up-command"
        :value="command"
        type="text"
        :readonly="launchMode !== 'command'"
        :class="[inputClass, launchMode !== 'command' ? 'bg-gray-50 text-gray-500 dark:bg-white/5' : '']"
        :placeholder="commandPlaceholder"
        @input="emit('update:command', ($event.target as HTMLInputElement).value)"
      />
      <p v-if="launchMode !== 'command'" :class="helpClass">
        由所选脚本或目录入口自动生成；执行仍经过 stdio 命令白名单校验。
      </p>
      <p v-if="fieldErrors.command" :class="errorClass">{{ fieldErrors.command }}</p>
    </div>
    <div>
      <label for="up-args" :class="labelClass">命令参数</label>
      <textarea
        id="up-args"
        :value="args"
        rows="4"
        :readonly="launchMode !== 'command'"
        :class="[
          textareaClass,
          launchMode !== 'command' ? 'bg-gray-50 text-gray-500 dark:bg-white/5' : '',
        ]"
        placeholder="-y&#10;@modelcontextprotocol/server-filesystem&#10;D:\\data"
        @input="emit('update:args', ($event.target as HTMLTextAreaElement).value)"
      ></textarea>
      <p :class="helpClass">
        <template v-if="launchMode === 'script'"
          >首个参数固定为受管脚本版本路径，避免运行内容漂移。</template
        >
        <template v-else-if="launchMode === 'directory'"
          >由只读目录探测生成绝对入口路径，不通过 shell。</template
        >
        <template v-else
          >每行一个参数。需要在参数中放凭证时，选择自定义注入并写入 ${credential}。</template
        >
      </p>
      <p v-if="fieldErrors.args" :class="errorClass">{{ fieldErrors.args }}</p>
    </div>
  </div>
</template>
