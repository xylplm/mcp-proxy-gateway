<template>
  <div
    class="pointer-events-none fixed right-0 bottom-0 z-99 px-4 pb-4 transition-[left] duration-300 ease-in-out md:px-6"
    :style="barStyle"
  >
    <div
      class="pointer-events-auto mx-auto flex w-full max-w-5xl flex-col gap-3 rounded-xl border border-gray-200 bg-white/95 p-3 shadow-theme-lg backdrop-blur sm:flex-row sm:items-center sm:justify-between dark:border-gray-800 dark:bg-gray-900/95"
    >
      <div class="min-w-0">
        <slot name="info" />
      </div>
      <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
        <slot />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useSidebar } from '@/composables/useSidebar'

const { width } = useBreakpoint()
const { isExpanded, isHovered } = useSidebar()

// 固定工具栏应在主内容页内居中，桌面端需要避开左侧侧边栏占位。
const sidebarOffset = computed(() => {
  if (width.value < 1024) return 0
  return isExpanded.value || isHovered.value ? 290 : 90
})

const barStyle = computed(() => ({
  left: `${sidebarOffset.value}px`,
}))
</script>
