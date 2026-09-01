<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { ErrorIcon, RefreshIcon, SuccessIcon } from '@/icons'
import type { AIProvider, ProviderTestResult } from '@/api/aiRisk'

type ProviderTestState = 'testing' | 'success' | 'error'

const props = defineProps<{
  open: boolean
  provider: AIProvider | null
  state: ProviderTestState
  result: ProviderTestResult | null
  errorMessage: string
  elapsedMs: number
}>()

const emit = defineEmits<{
  close: []
  retry: []
}>()

const dialogRef = ref<HTMLElement | null>(null)
const providerTimeoutMs = computed(() => Math.max((props.provider?.timeoutS ?? 60) * 1000, 1000))
const displayDurationMs = computed(() =>
  props.state === 'success' && props.result !== null ? props.result.latencyMs : props.elapsedMs,
)
const timeoutBudgetProgress = computed(() =>
  Math.min(100, Math.round((props.elapsedMs / providerTimeoutMs.value) * 100)),
)
const hasTokenUsage = computed(
  () =>
    props.result?.inputTokens !== undefined ||
    props.result?.outputTokens !== undefined ||
    props.result?.totalTokens !== undefined,
)
const tokenFormatter = new Intl.NumberFormat('zh-CN')
let activeElementBeforeOpen: HTMLElement | null = null

function closeOnEscape(event: KeyboardEvent): void {
  if (event.key !== 'Escape' || !props.open) return
  event.preventDefault()
  emit('close')
}

function removeKeyboardListener(): void {
  window.removeEventListener('keydown', closeOnEscape)
}

watch(
  () => props.open,
  async (open) => {
    if (!open) {
      removeKeyboardListener()
      activeElementBeforeOpen?.focus({ preventScroll: true })
      activeElementBeforeOpen = null
      return
    }
    activeElementBeforeOpen =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    window.addEventListener('keydown', closeOnEscape)
    await nextTick()
    if (props.open) dialogRef.value?.focus({ preventScroll: true })
  },
)

onBeforeUnmount(removeKeyboardListener)

function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${milliseconds} ms`
  return `${(milliseconds / 1000).toFixed(milliseconds < 10000 ? 1 : 0)} 秒`
}

function formatTokens(value: number | undefined): string {
  return value === undefined ? '未返回' : tokenFormatter.format(value)
}

function apiStyleLabel(provider: AIProvider): string {
  return provider.apiStyle === 'responses' ? 'Responses' : 'Chat Completions'
}
</script>

<template>
  <transition name="provider-test-modal">
    <div
      v-if="open && provider !== null"
      ref="dialogRef"
      class="4k:p-8 fixed inset-0 z-[100002] flex items-end justify-center bg-gray-950/45 p-0 backdrop-blur-[2px] sm:items-center sm:p-5"
      role="dialog"
      aria-modal="true"
      aria-labelledby="provider-test-title"
      tabindex="-1"
    >
      <div
        class="provider-test-panel shadow-theme-xl 3xl:max-w-3xl 4k:max-w-4xl flex max-h-[calc(100dvh-0.5rem)] w-full max-w-2xl flex-col overflow-hidden rounded-t-[1.75rem] border border-white/70 bg-white text-gray-800 sm:max-h-[88dvh] sm:rounded-[1.75rem] dark:border-white/[0.06] dark:bg-gray-900 dark:text-gray-200"
      >
        <div
          class="from-brand-50/70 dark:from-brand-500/[0.09] flex items-start justify-between gap-4 border-b border-gray-200 bg-gradient-to-br via-white to-white px-4 py-4 sm:px-6 sm:py-5 dark:border-gray-800 dark:via-gray-900 dark:to-gray-900"
        >
          <div class="min-w-0">
            <h3
              id="provider-test-title"
              class="truncate text-lg font-semibold tracking-tight text-gray-900 dark:text-white"
            >
              测试 AI Provider
            </h3>
            <p class="mt-1 truncate text-sm text-gray-500 dark:text-gray-400">
              {{ provider.name }} · {{ provider.model }}
            </p>
          </div>
          <button
            v-tooltip:bottom-end="state === 'testing' ? '停止并关闭测试' : '关闭'"
            type="button"
            class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-gray-400 transition hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-white/[0.06] dark:hover:text-gray-200"
            :aria-label="state === 'testing' ? '停止并关闭测试' : '关闭测试结果'"
            @click="emit('close')"
          >
            <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none">
              <path
                d="M6 6l12 12M6 18L18 6"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>

        <div class="custom-scrollbar overflow-y-auto px-4 py-5 sm:px-6">
          <div
            class="xsm:grid-cols-2 grid gap-2 rounded-2xl border border-gray-100 bg-gray-50/80 p-2 text-sm sm:grid-cols-[minmax(0,0.65fr)_minmax(0,1.5fr)_auto] dark:border-gray-800 dark:bg-white/[0.03]"
          >
            <div class="min-w-0 rounded-xl bg-white/70 px-3 py-2.5 dark:bg-white/[0.03]">
              <p class="text-xs text-gray-500 dark:text-gray-400">接口协议</p>
              <p class="mt-1 truncate font-medium text-gray-800 dark:text-gray-100">
                {{ apiStyleLabel(provider) }}
              </p>
            </div>
            <div class="min-w-0 rounded-xl bg-white/70 px-3 py-2.5 dark:bg-white/[0.03]">
              <p class="text-xs text-gray-500 dark:text-gray-400">Base URL</p>
              <p class="mt-1 font-medium break-all text-gray-800 dark:text-gray-100">
                {{ provider.baseUrl }}
              </p>
            </div>
            <div
              class="xsm:col-span-2 min-w-0 rounded-xl bg-white/70 px-3 py-2.5 sm:col-span-1 dark:bg-white/[0.03]"
            >
              <p class="text-xs text-gray-500 dark:text-gray-400">超时预算</p>
              <p class="mt-1 font-medium text-gray-800 dark:text-gray-100">
                {{ provider.timeoutS }} 秒
              </p>
            </div>
          </div>

          <section v-if="state === 'testing'" class="mt-5" role="status" aria-live="polite">
            <div class="flex items-center gap-4 sm:gap-5">
              <div
                class="relative flex h-16 w-16 shrink-0 items-center justify-center"
                aria-hidden="true"
              >
                <span
                  class="border-brand-200/80 dark:border-brand-500/25 absolute inset-1 rounded-full border"
                ></span>
                <span
                  class="provider-test-orbit border-brand-300/50 dark:border-brand-400/25 absolute inset-0 rounded-full border"
                >
                  <span
                    class="bg-brand-500 absolute -top-1 left-1/2 h-2 w-2 -translate-x-1/2 rounded-full shadow-[0_0_0_4px_rgb(70_95_255_/_0.14)]"
                  ></span>
                </span>
                <span
                  class="from-brand-50 to-brand-100 text-brand-600 dark:from-brand-500/20 dark:to-brand-500/5 dark:text-brand-300 relative flex h-11 w-11 items-center justify-center rounded-full bg-gradient-to-br shadow-sm"
                >
                  <RefreshIcon class="h-5 w-5" />
                </span>
              </div>
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <h4 class="font-semibold text-gray-900 dark:text-white">正在测试连接</h4>
                  <span
                    class="text-brand-600 dark:text-brand-300 inline-flex items-center gap-1.5 text-xs font-medium"
                  >
                    <span
                      class="bg-brand-500 h-1.5 w-1.5 animate-pulse rounded-full motion-reduce:animate-none"
                    ></span>
                    已发起请求
                  </span>
                </div>
                <p class="mt-1 text-sm leading-5 text-gray-500 dark:text-gray-400">
                  正在向模型发送测试请求并校验响应格式；可在此查看实时状态，或随时停止测试。
                </p>
              </div>
            </div>

            <div class="mt-6">
              <div
                class="flex items-center justify-between gap-3 text-xs text-gray-500 dark:text-gray-400"
              >
                <span>已用 {{ formatDuration(elapsedMs) }}</span>
                <span>{{ formatDuration(providerTimeoutMs) }} 超时预算</span>
              </div>
              <div
                class="mt-2 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800"
                role="progressbar"
                aria-label="Provider 测试超时预算"
                :aria-valuemin="0"
                :aria-valuemax="providerTimeoutMs"
                :aria-valuenow="Math.min(elapsedMs, providerTimeoutMs)"
                :aria-valuetext="`已使用 ${formatDuration(elapsedMs)}，超时预算 ${formatDuration(providerTimeoutMs)}`"
              >
                <span
                  class="from-brand-500 to-brand-400 block h-full rounded-full bg-gradient-to-r transition-[width] duration-300 ease-out motion-reduce:transition-none"
                  :style="{ width: `${timeoutBudgetProgress}%` }"
                ></span>
              </div>
              <p class="mt-2 text-xs leading-5 text-gray-400 dark:text-gray-500">
                测试会在上方显示的超时预算内继续等待；如需停止，可随时关闭此窗口。
              </p>
            </div>
          </section>

          <section
            v-else-if="state === 'success' && result !== null"
            class="provider-test-result border-success-200 bg-success-50/70 dark:border-success-500/20 dark:bg-success-500/10 mt-5 rounded-2xl border p-4 sm:p-5"
            role="status"
            aria-live="polite"
          >
            <div class="flex items-start gap-3">
              <SuccessIcon class="text-success-500 mt-0.5 h-5 w-5 shrink-0" aria-hidden="true" />
              <div class="min-w-0">
                <h4 class="text-success-700 dark:text-success-300 font-semibold">连接测试成功</h4>
                <p class="text-success-700/90 dark:text-success-300/90 mt-1 text-sm leading-5">
                  模型已完成测试请求，返回内容通过了结构校验。
                </p>
              </div>
            </div>

            <div class="mt-4 grid grid-cols-2 gap-2 sm:gap-3 lg:grid-cols-4">
              <div class="rounded-xl bg-white/75 p-3 dark:bg-white/[0.06]">
                <p class="text-xs text-gray-500 dark:text-gray-400">响应延迟</p>
                <p class="mt-1 text-lg font-semibold tracking-tight text-gray-900 dark:text-white">
                  {{ formatDuration(result.latencyMs) }}
                </p>
              </div>
              <div class="rounded-xl bg-white/75 p-3 dark:bg-white/[0.06]">
                <p class="text-xs text-gray-500 dark:text-gray-400">输入 Token</p>
                <p class="mt-1 text-lg font-semibold tracking-tight text-gray-900 dark:text-white">
                  {{ formatTokens(result.inputTokens) }}
                </p>
              </div>
              <div class="rounded-xl bg-white/75 p-3 dark:bg-white/[0.06]">
                <p class="text-xs text-gray-500 dark:text-gray-400">输出 Token</p>
                <p class="mt-1 text-lg font-semibold tracking-tight text-gray-900 dark:text-white">
                  {{ formatTokens(result.outputTokens) }}
                </p>
              </div>
              <div class="rounded-xl bg-white/75 p-3 dark:bg-white/[0.06]">
                <p class="text-xs text-gray-500 dark:text-gray-400">总 Token</p>
                <p class="mt-1 text-lg font-semibold tracking-tight text-gray-900 dark:text-white">
                  {{ formatTokens(result.totalTokens) }}
                </p>
              </div>
            </div>
            <p
              v-if="!hasTokenUsage"
              class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400"
            >
              当前 Provider 未在响应中返回 Token 用量；连接与响应格式仍已验证通过。
            </p>
          </section>

          <section
            v-else
            class="provider-test-result border-error-200 bg-error-50/70 dark:border-error-500/20 dark:bg-error-500/10 mt-5 rounded-2xl border p-4 sm:p-5"
            role="alert"
          >
            <div class="flex items-start gap-3">
              <ErrorIcon class="text-error-500 mt-0.5 h-5 w-5 shrink-0" aria-hidden="true" />
              <div class="min-w-0">
                <h4 class="text-error-700 dark:text-error-300 font-semibold">测试未通过</h4>
                <p
                  class="text-error-700/90 dark:text-error-300/90 mt-1 text-sm leading-5 break-words"
                >
                  {{ errorMessage || 'Provider 未返回可用的测试结果。' }}
                </p>
                <p class="mt-3 text-xs leading-5 text-gray-600 dark:text-gray-300">
                  已等待 {{ formatDuration(displayDurationMs) }}。请检查 Base URL、API
                  Key、模型名称和接口协议后重试。
                </p>
              </div>
            </div>
          </section>
        </div>

        <div
          class="flex flex-col-reverse gap-2 border-t border-gray-200 px-4 py-4 sm:flex-row sm:justify-end sm:px-6 dark:border-gray-800"
        >
          <button
            v-if="state === 'testing'"
            type="button"
            class="inline-flex items-center justify-center rounded-xl border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/[0.06]"
            @click="emit('close')"
          >
            停止测试
          </button>
          <template v-else>
            <button
              type="button"
              class="rounded-xl border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/[0.06]"
              @click="emit('close')"
            >
              关闭
            </button>
            <button
              type="button"
              class="bg-brand-500 hover:bg-brand-600 inline-flex items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium text-white transition"
              @click="emit('retry')"
            >
              <RefreshIcon class="h-4 w-4" aria-hidden="true" />
              {{ state === 'success' ? '再次测试' : '重新测试' }}
            </button>
          </template>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.provider-test-modal-enter-active,
.provider-test-modal-leave-active {
  transition: opacity 0.2s ease;
}

.provider-test-modal-enter-active .provider-test-panel,
.provider-test-modal-leave-active .provider-test-panel {
  transition:
    transform 0.2s ease,
    opacity 0.2s ease;
}

.provider-test-modal-enter-from,
.provider-test-modal-leave-to {
  opacity: 0;
}

.provider-test-modal-enter-from .provider-test-panel,
.provider-test-modal-leave-to .provider-test-panel {
  transform: translateY(8px) scale(0.98);
  opacity: 0;
}

.provider-test-orbit {
  animation: provider-test-orbit 2.4s cubic-bezier(0.45, 0, 0.55, 1) infinite;
  will-change: transform;
}

.provider-test-result {
  animation: provider-test-result-in 0.32s cubic-bezier(0.16, 1, 0.3, 1) both;
}

@keyframes provider-test-orbit {
  to {
    transform: rotate(360deg);
  }
}

@keyframes provider-test-result-in {
  from {
    transform: translateY(6px);
    opacity: 0;
  }

  to {
    transform: translateY(0);
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .provider-test-modal-enter-active,
  .provider-test-modal-leave-active,
  .provider-test-modal-enter-active .provider-test-panel,
  .provider-test-modal-leave-active .provider-test-panel {
    transition: none;
  }

  .provider-test-orbit,
  .provider-test-result {
    animation: none;
  }
}
</style>
