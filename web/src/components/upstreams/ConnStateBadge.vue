<script setup lang="ts">
/**
 * 连接状态徽章（TailAdmin 风格 badge）。
 * 依据上游连接状态渲染不同配色：可用=绿，连接中=蓝，不可用=红，低频恢复=橙。
 * 覆盖 Req 2.8（连接状态展示）。
 */
import { computed } from 'vue'
import { CONN_STATE_LABELS, type ConnState } from '@/api/upstreams'

const props = defineProps<{ state: ConnState }>()

/** 各状态对应的徽章配色类（含暗色模式）。 */
const STATE_CLASSES: Record<ConnState, string> = {
  available: 'bg-success-50 text-success-700 dark:bg-success-500/15 dark:text-success-400',
  connecting: 'bg-blue-light-50 text-blue-light-700 dark:bg-blue-light-500/15 dark:text-blue-light-400',
  unavailable: 'bg-error-50 text-error-700 dark:bg-error-500/15 dark:text-error-400',
  suspended: 'bg-warning-50 text-warning-700 dark:bg-warning-500/15 dark:text-warning-400',
}

/** 状态指示圆点配色。 */
const DOT_CLASSES: Record<ConnState, string> = {
  available: 'bg-success-500',
  connecting: 'bg-blue-light-500',
  unavailable: 'bg-error-500',
  suspended: 'bg-warning-500',
}

const badgeClass = computed(() => STATE_CLASSES[props.state] ?? STATE_CLASSES.unavailable)
const dotClass = computed(() => DOT_CLASSES[props.state] ?? DOT_CLASSES.unavailable)
const label = computed(() => CONN_STATE_LABELS[props.state] ?? props.state)
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium"
    :class="badgeClass"
  >
    <span class="h-1.5 w-1.5 rounded-full" :class="dotClass"></span>
    {{ label }}
  </span>
</template>
