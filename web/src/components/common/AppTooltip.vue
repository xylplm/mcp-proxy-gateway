<script setup lang="ts">
import type { TooltipPlacement } from '@/directives/tooltip'

const props = withDefaults(defineProps<{
  content: string
  placement?: TooltipPlacement
  disabled?: boolean
  tag?: keyof HTMLElementTagNameMap
}>(), {
  placement: 'top',
  disabled: false,
  tag: 'span',
})
</script>

<template>
  <component
    :is="props.tag"
    v-tooltip="{ content: props.content, placement: props.placement, disabled: props.disabled }"
    class="inline-flex"
  >
    <slot />
  </component>
</template>