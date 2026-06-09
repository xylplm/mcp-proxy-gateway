<script setup lang="ts">
import InfoCircleIcon from '@/icons/InfoCircleIcon.vue'

withDefaults(
  defineProps<{
    label: string
    required?: boolean
    tooltip?: string
    forId?: string
  }>(),
  {
    required: false,
    tooltip: '',
    forId: undefined,
  },
)
</script>

<template>
  <div class="mb-1.5 flex items-center gap-1.5">
    <label
      :for="forId"
      class="text-sm font-medium text-gray-700 dark:text-gray-400"
    >
      {{ label }}
      <span v-if="required" class="text-error-500" aria-label="必填">*</span>
    </label>
    <button
      v-if="tooltip !== ''"
      v-tooltip:top="tooltip"
      type="button"
      class="inline-flex h-5 w-5 items-center justify-center rounded-full text-gray-400 transition hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-white/5 dark:hover:text-gray-300"
      :aria-label="`${label}说明`"
    >
      <InfoCircleIcon class="h-4 w-4" aria-hidden="true" />
    </button>
  </div>
</template>
