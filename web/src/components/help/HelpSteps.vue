<template>
  <!--
    编号步骤：傻瓜式引导的主要载体。
    竖线连接相邻步骤，最后一步不画线；小屏单列，文字自动换行。
  -->
  <ol class="space-y-0">
    <li v-for="(step, index) in steps" :key="step.title" class="flex gap-4">
      <div class="flex flex-col items-center">
        <span
          class="from-brand-500 to-brand-600 flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-gradient-to-br text-sm font-semibold text-white shadow-sm"
          aria-hidden="true"
        >
          {{ index + 1 }}
        </span>
        <span
          v-if="index < steps.length - 1"
          class="from-brand-200 dark:from-brand-500/30 mt-1 w-px flex-1 bg-gradient-to-b to-transparent"
        ></span>
      </div>
      <div class="min-w-0 flex-1" :class="index < steps.length - 1 ? 'pb-6' : ''">
        <p class="font-medium text-gray-800 dark:text-white/90">{{ step.title }}</p>
        <p class="mt-1 text-sm leading-7 text-gray-600 dark:text-gray-300">{{ step.desc }}</p>
        <p v-if="step.hint" class="mt-1.5 text-xs leading-6 text-gray-400 dark:text-gray-500">
          {{ step.hint }}
        </p>
      </div>
    </li>
  </ol>
</template>

<script setup lang="ts">
export interface HelpStep {
  title: string
  desc: string
  /** 补充说明，比正文更淡；用于放路径、字段名等细节 */
  hint?: string
}

defineProps<{ steps: HelpStep[] }>()
</script>
