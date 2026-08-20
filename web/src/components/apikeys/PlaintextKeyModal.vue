<script setup lang="ts">
/**
 * 一次性明文密钥展示弹窗（任务 26.3，Req 12.3）。
 *
 * 创建 API Key 成功后展示完整明文密钥（plaintextKey）并提供一键复制。
 * 自部署场景下密钥支持二次查看，故此弹窗仅作创建后的便捷展示，关闭后仍可在列表中查看。
 */
import type { CreatedAPIKey } from '@/api/apikeys'
import { useClipboard } from '@/composables/useClipboard'
import { useToast } from '@/composables/useToast'

const props = defineProps<{
  /** 创建成功的 API Key（含一次性明文）；为 null 时不渲染。 */
  created: CreatedAPIKey | null
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

/** 复制状态提示（复制成功后短暂置位，由 useClipboard 自动复位）。 */
const { copied, copy } = useClipboard()
const toast = useToast()

/**
 * 复制明文密钥到剪贴板（Req 12.3）。
 *
 * 走 useClipboard：navigator.clipboard 仅在安全上下文可用，局域网 http 访问时
 * 需降级到 execCommand，否则按钮会毫无反应。复制结果必须给出可见反馈。
 */
async function copyKey(): Promise<void> {
  if (props.created === null) return
  const ok = await copy(props.created.plaintextKey)
  if (ok) toast.success('已复制到剪贴板')
  else toast.error('复制失败，请手动选中密钥复制')
}

/** 关闭弹窗并重置复制态。 */
function close(): void {
  copied.value = false
  emit('close')
}
</script>

<template>
  <transition name="fade">
    <div
      v-if="created !== null"
      class="fixed inset-0 z-[100002] flex items-center justify-center bg-gray-900/50 p-4 backdrop-blur-[1px]"
    >
      <div
        class="w-full max-w-lg rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <div class="mb-4 flex items-start gap-3">
          <span
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-warning-50 text-warning-500 dark:bg-warning-500/10"
          >
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
              <path
                d="M12 9v4m0 4h.01M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </span>
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">
              API Key 创建成功
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              请妥善保存。你也可以稍后在列表中通过「查看」随时复制该密钥。
            </p>
          </div>
        </div>

        <!-- 名称 -->
        <p class="mb-2 text-sm text-gray-500 dark:text-gray-400">
          名称：<span class="font-medium text-gray-800 dark:text-white/90">{{ created.name }}</span>
        </p>

        <!-- 明文密钥 + 复制 -->
        <div
          class="mb-5 flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-700 dark:bg-gray-800"
        >
          <code
            class="flex-1 overflow-x-auto font-mono text-sm break-all text-gray-800 dark:text-gray-200"
          >
            {{ created.plaintextKey }}
          </code>
          <button
            type="button"
            class="inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-brand-500 px-3 py-2 text-xs font-medium text-white transition hover:bg-brand-600"
            @click="copyKey"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
              <path
                d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2v-2M8 4H6a2 2 0 0 0-2 2v2m4-6h6a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Z"
                stroke="currentColor"
                stroke-width="1.6"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
            {{ copied ? '已复制' : '复制' }}
          </button>
        </div>

        <div class="flex justify-end">
          <button
            type="button"
            class="rounded-lg bg-brand-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
            @click="close"
          >
            我已保存，关闭
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
