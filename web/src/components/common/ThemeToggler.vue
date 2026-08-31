<template>
  <div ref="root" class="relative">
    <button
      v-tooltip:bottom="`主题：${themeLabel[themeMode]}`"
      type="button"
      class="relative flex h-11 w-11 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-white"
      :aria-label="`选择主题，当前为${themeLabel[themeMode]}`"
      aria-haspopup="menu"
      :aria-expanded="open"
      @click="open = !open"
    >
      <svg
        v-if="themeMode === 'system'"
        aria-hidden="true"
        class="h-5 w-5"
        viewBox="0 0 24 24"
        fill="none"
      >
        <path
          d="M4 5.75h16v10.5H4zM9 20h6m-3-3.75V20"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
      <svg
        v-else-if="themeMode === 'light'"
        aria-hidden="true"
        class="h-5 w-5"
        viewBox="0 0 24 24"
        fill="none"
      >
        <circle cx="12" cy="12" r="4" stroke="currentColor" stroke-width="1.7" />
        <path
          d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4m10.6 10.6 1.4 1.4m0-13.4-1.4 1.4M6.7 17.3l-1.4 1.4"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
        />
      </svg>
      <svg v-else aria-hidden="true" class="h-5 w-5" viewBox="0 0 24 24" fill="none">
        <path
          d="M20.2 15.2A8.5 8.5 0 0 1 8.8 3.8a8.5 8.5 0 1 0 11.4 11.4Z"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linejoin="round"
        />
      </svg>
    </button>

    <div
      v-if="open"
      role="menu"
      class="absolute right-0 z-[100002] mt-2 w-40 rounded-lg border border-gray-200 bg-white p-1.5 shadow-lg dark:border-gray-700 dark:bg-gray-900"
    >
      <button
        v-for="option in themeOptions"
        :key="option.value"
        type="button"
        role="menuitemradio"
        :aria-checked="themeMode === option.value"
        class="flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
        :class="
          themeMode === option.value
            ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
            : ''
        "
        @click="chooseTheme(option.value)"
      >
        <span>{{ option.label }}</span>
        <span v-if="themeMode === option.value" aria-hidden="true">✓</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useTheme } from '../layout/ThemeProvider.vue'
import type { ThemeMode } from '@/utils/theme'

const { themeMode, setThemeMode } = useTheme()
const root = ref<HTMLElement | null>(null)
const open = ref(false)
const themeLabel: Record<ThemeMode, string> = {
  system: '跟随系统',
  light: '浅色',
  dark: '深色',
}
const themeOptions = (Object.entries(themeLabel) as [ThemeMode, string][]).map(
  ([value, label]) => ({ value, label }),
)

function chooseTheme(mode: ThemeMode): void {
  setThemeMode(mode)
  open.value = false
}

function closeOnOutsideClick(event: MouseEvent): void {
  if (!root.value?.contains(event.target as Node)) open.value = false
}

function closeOnEscape(event: KeyboardEvent): void {
  if (event.key === 'Escape') open.value = false
}

onMounted(() => {
  document.addEventListener('click', closeOnOutsideClick)
  document.addEventListener('keydown', closeOnEscape)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', closeOnOutsideClick)
  document.removeEventListener('keydown', closeOnEscape)
})
</script>
