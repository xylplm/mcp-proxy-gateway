<script setup lang="ts">
/**
 * 创建 API Key 弹窗（任务 26.3，Req 12.1、12.6）。
 *
 * 表单字段：名称（必填，1-100 字符）、可选有效期 expiresAt（留空表示永不过期）。
 * 提交成功后由父组件接收 CreatedAPIKey 并弹出一次性明文密钥展示弹窗（Req 12.3）。
 */
import { ref, watch } from 'vue'
import { createAPIKey, type CreatedAPIKey } from '@/api/apikeys'

const props = defineProps<{ open: boolean }>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'created', created: CreatedAPIKey): void
}>()

/** 表单字段。 */
const name = ref('')
/** datetime-local 输入值（本地时区），提交时转换为 RFC3339。 */
const expiresAtLocal = ref('')

/** 提交状态与错误。 */
const submitting = ref(false)
const errorMessage = ref('')

/** 打开时重置表单。 */
watch(
  () => props.open,
  (open) => {
    if (open) {
      name.value = ''
      expiresAtLocal.value = ''
      errorMessage.value = ''
      submitting.value = false
    }
  },
)

/** 提交创建（Req 12.1）。 */
async function submit(): Promise<void> {
  const trimmed = name.value.trim()
  if (trimmed.length < 1 || trimmed.length > 100) {
    errorMessage.value = '名称长度需在 1 至 100 个字符之间'
    return
  }
  submitting.value = true
  errorMessage.value = ''
  try {
    // datetime-local 无时区信息，交由 Date 解析为本地时间后转 RFC3339（ISO）。
    const expiresAt =
      expiresAtLocal.value !== '' ? new Date(expiresAtLocal.value).toISOString() : null
    const created = await createAPIKey({ name: trimmed, expiresAt })
    emit('created', created)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '创建 API Key 失败'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <transition name="fade">
    <div
      v-if="open"
      class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
    >
      <div
        class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <h3 class="mb-5 text-base font-semibold text-gray-800 dark:text-white/90">
          新建 API Key
        </h3>

        <p
          v-if="errorMessage !== ''"
          class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ errorMessage }}
        </p>

        <div class="mb-4">
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
            名称 <span class="text-error-500">*</span>
          </label>
          <input
            v-model="name"
            type="text"
            maxlength="100"
            placeholder="如：生产环境客户端"
            class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
            @keyup.enter="submit"
          />
        </div>

        <div class="mb-6">
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
            有效期
          </label>
          <input
            v-model="expiresAtLocal"
            type="datetime-local"
            class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
          />
          <p class="mt-1 text-xs text-gray-400">留空表示永不过期。</p>
        </div>

        <div class="flex justify-end gap-3">
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="submitting"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            class="rounded-lg bg-brand-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
            :disabled="submitting"
            @click="submit"
          >
            {{ submitting ? '创建中…' : '创建' }}
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
