<template>
  <!-- 提示块：info 解释概念，success 推荐做法，warning 易错点，danger 有破坏性。 -->
  <div class="rounded-xl border px-4 py-3 text-sm leading-7" :class="toneClass">
    <p v-if="title" class="mb-1 font-semibold">{{ title }}</p>
    <div><slot /></div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    tone?: 'info' | 'success' | 'warning' | 'danger'
    title?: string
  }>(),
  { tone: 'info' },
)

const toneClass = computed(() => {
  switch (props.tone) {
    case 'success':
      return 'border-success-200 bg-success-50/70 text-success-800 dark:border-success-500/30 dark:bg-success-500/10 dark:text-success-300'
    case 'warning':
      return 'border-warning-200 bg-warning-50/70 text-warning-800 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300'
    case 'danger':
      return 'border-error-200 bg-error-50/70 text-error-700 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-300'
    default:
      return 'border-brand-200 bg-brand-50/70 text-brand-800 dark:border-brand-500/30 dark:bg-brand-500/10 dark:text-brand-200'
  }
})
</script>
