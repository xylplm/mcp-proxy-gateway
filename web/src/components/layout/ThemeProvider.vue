<template>
  <slot></slot>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import { parseThemeMode, resolveTheme, THEME_STORAGE_KEY, type ThemeMode } from '@/utils/theme'

const systemQuery = window.matchMedia('(prefers-color-scheme: dark)')
const themeMode = ref<ThemeMode>(readStoredTheme())
const systemPrefersDark = ref(systemQuery.matches)
const resolvedTheme = computed(() => resolveTheme(themeMode.value, systemPrefersDark.value))
const isDarkMode = computed(() => resolvedTheme.value === 'dark')

function readStoredTheme(): ThemeMode {
  try {
    return parseThemeMode(localStorage.getItem(THEME_STORAGE_KEY))
  } catch {
    return 'system'
  }
}

function setThemeMode(mode: ThemeMode): void {
  themeMode.value = mode
}

function toggleTheme(): void {
  setThemeMode(resolvedTheme.value === 'dark' ? 'light' : 'dark')
}

function handleSystemThemeChange(event: MediaQueryListEvent): void {
  systemPrefersDark.value = event.matches
}

watch(
  [themeMode, resolvedTheme],
  ([mode, theme]) => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
    document.documentElement.dataset.theme = mode
    try {
      localStorage.setItem(THEME_STORAGE_KEY, mode)
    } catch {
      // 浏览器禁用本地存储时仍保留当前会话内的主题选择。
    }
  },
  { immediate: true },
)

onMounted(() => systemQuery.addEventListener('change', handleSystemThemeChange))
onBeforeUnmount(() => systemQuery.removeEventListener('change', handleSystemThemeChange))

provide('theme', { themeMode, resolvedTheme, isDarkMode, setThemeMode, toggleTheme })
</script>

<script lang="ts">
import { inject } from 'vue'
import type { ComputedRef, Ref } from 'vue'
import type { ResolvedTheme, ThemeMode as ThemeModeValue } from '@/utils/theme'

interface ThemeContext {
  themeMode: Ref<ThemeModeValue>
  resolvedTheme: ComputedRef<ResolvedTheme>
  isDarkMode: ComputedRef<boolean>
  setThemeMode: (mode: ThemeModeValue) => void
  toggleTheme: () => void
}

export function useTheme(): ThemeContext {
  const theme = inject<ThemeContext>('theme')
  if (!theme) throw new Error('useTheme must be used within a ThemeProvider')
  return theme
}
</script>
