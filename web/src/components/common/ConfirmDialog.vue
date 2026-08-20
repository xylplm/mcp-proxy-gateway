<script setup lang="ts">
import { computed } from 'vue'
import { useConfirm, type ConfirmTone } from '@/composables/useConfirm'

const { confirmState, resolveConfirm } = useConfirm()

const confirmClass: Record<ConfirmTone, string> = {
  danger: 'bg-error-500 hover:bg-error-600 text-white',
  warning: 'bg-warning-500 hover:bg-warning-600 text-white',
  info: 'bg-brand-500 hover:bg-brand-600 text-white',
}

const iconClass: Record<ConfirmTone, string> = {
  danger: 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400',
  warning: 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400',
  info: 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400',
}

const state = computed(() => confirmState.value)
</script>

<template>
  <transition name="fade">
    <div
      v-if="state.open"
      class="fixed inset-0 z-[100040] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
    >
      <div class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
        <div class="flex items-start gap-4">
          <span class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl" :class="iconClass[state.tone]">
            !
          </span>
          <div class="min-w-0">
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">{{ state.title }}</h3>
            <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-gray-500 dark:text-gray-400">{{ state.message }}</p>
          </div>
        </div>
        <div class="mt-6 flex justify-end gap-3">
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="resolveConfirm(false)"
          >
            {{ state.cancelText }}
          </button>
          <button
            type="button"
            class="rounded-lg px-4 py-2 text-sm font-medium transition"
            :class="confirmClass[state.tone]"
            @click="resolveConfirm(true)"
          >
            {{ state.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.18s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
