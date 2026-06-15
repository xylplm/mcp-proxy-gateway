<script setup lang="ts">
import type { TooltipPlacement } from '@/directives/tooltip'

const props = withDefaults(defineProps<{
  content: string
  placement?: TooltipPlacement
  disabled?: boolean
  wrap?: boolean
  tag?: keyof HTMLElementTagNameMap
}>(), {
  placement: 'top',
  disabled: false,
  wrap: false,
  tag: 'span',
})
</script>

<template>
  <component
    :is="props.tag"
    v-tooltip="{ content: props.content, placement: props.placement, disabled: props.disabled, wrap: props.wrap }"
    class="inline-flex"
  >
    <slot />
  </component>
</template>