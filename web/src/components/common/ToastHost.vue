<script setup lang="ts">
import { computed } from 'vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useSidebar } from '@/composables/useSidebar'
import { useToast, type ToastType } from '@/composables/useToast'

const { toasts } = useToast()
const { width } = useBreakpoint()
const { isExpanded, isHovered } = useSidebar()

const typeClass: Record<ToastType, string> = {
  success: 'text-success-500',
  warning: 'text-warning-500',
  error: 'text-error-500',
  info: 'text-brand-500',
}

const iconPath: Record<ToastType, string> = {
  success: 'M8.2 12.7 5.35 9.85l-1.2 1.2L8.2 15.1 15.85 7.45l-1.2-1.2L8.2 12.7Z',
  warning: 'M10 5.6 3.6 16.5h12.8L10 5.6Zm0 3.2c.42 0 .76.34.76.76v3.18a.76.76 0 0 1-1.52 0V9.56c0-.42.34-.76.76-.76Zm0 6.9a.9.9 0 1 1 0-1.8.9.9 0 0 1 0 1.8Z',
  error: 'm10 8.82 3.18-3.18 1.18 1.18L11.18 10l3.18 3.18-1.18 1.18L10 11.18l-3.18 3.18-1.18-1.18L8.82 10 5.64 6.82l1.18-1.18L10 8.82Z',
  info: 'M10 5.2a.95.95 0 1 1 0 1.9.95.95 0 0 1 0-1.9Zm-.75 3.3h1.5v6.3h-1.5V8.5Z',
}

const item = computed(() => toasts.value[0] ?? null)

const sidebarOffset = computed(() => {
  if (width.value < 1024) return 0
  return isExpanded.value || isHovered.value ? 290 : 90
})

const hostStyle = computed(() => ({
  left: `${sidebarOffset.value}px`,
}))
</script>

<template>
  <div
    class="pointer-events-none fixed top-5 right-0 z-[100010] flex justify-center px-4 transition-[left] duration-300 ease-in-out sm:top-6"
    :style="hostStyle"
  >
    <transition name="toast">
      <div
        v-if="item !== null"
        :key="item.id"
        class="inline-flex min-h-12 max-w-[min(92vw,34rem)] items-center gap-2.5 rounded-lg border border-gray-100 bg-white px-5 py-3 text-sm font-medium text-gray-800 shadow-theme-lg dark:border-gray-800 dark:bg-gray-900 dark:text-white/90"
        role="status"
      >
        <span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full" :class="typeClass[item.type]">
          <svg viewBox="0 0 20 20" class="h-5 w-5" aria-hidden="true">
            <circle cx="10" cy="10" r="8" fill="none" stroke="currentColor" stroke-width="1.7" />
            <path :d="iconPath[item.type]" fill="currentColor" />
          </svg>
        </span>
        <span class="min-w-0 break-words leading-5">{{ item.message }}</span>
      </div>
    </transition>
  </div>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition:
    opacity 0.16s ease,
    transform 0.16s ease;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.98);
}
</style>
