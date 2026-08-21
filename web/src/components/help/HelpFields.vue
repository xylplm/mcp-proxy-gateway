<template>
  <!--
    字段/选项说明：用描述列表而不是表格，小屏天然可读，不需要横向滚动。
    ≥sm 时名称与说明并排，名称列固定宽度形成对齐感。
  -->
  <dl class="divide-y divide-gray-100 dark:divide-white/5">
    <div v-for="item in items" :key="item.name" class="py-3 sm:flex sm:gap-6">
      <dt class="sm:w-52 sm:shrink-0">
        <span class="font-mono text-sm font-medium break-all text-gray-800 dark:text-white/90">
          {{ item.name }}
        </span>
        <span
          v-if="item.tag"
          class="ml-2 inline-flex rounded-full bg-gray-100 px-2 py-0.5 align-middle text-[11px] font-medium text-gray-600 dark:bg-white/10 dark:text-gray-300"
        >
          {{ item.tag }}
        </span>
      </dt>
      <dd class="mt-1 text-sm leading-7 text-gray-600 sm:mt-0 sm:flex-1 dark:text-gray-300">
        {{ item.desc }}
      </dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
export interface HelpField {
  name: string
  desc: string
  /** 可选徽标，如「必填」「默认 30s」 */
  tag?: string
}

defineProps<{ items: HelpField[] }>()
</script>
