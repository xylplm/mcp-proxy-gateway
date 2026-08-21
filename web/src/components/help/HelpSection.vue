<template>
  <!--
    帮助文档小节：统一标题层级、锚点与间距。
    带 id 便于从别处深链到具体小节（/help/upstreams#stdio）。
  -->
  <section :id="anchor" class="group/sec scroll-mt-24">
    <div class="flex items-baseline gap-2">
      <h2 class="text-lg font-semibold text-gray-800 sm:text-xl dark:text-white/90">
        {{ title }}
      </h2>
      <a
        v-if="anchor"
        :href="`#${anchor}`"
        class="hover:text-brand-500 text-sm text-gray-300 opacity-0 transition-opacity group-hover/sec:opacity-100 focus-visible:opacity-100 motion-reduce:transition-none dark:text-gray-600"
        :aria-label="`跳转到小节：${title}`"
        >#</a
      >
    </div>
    <p v-if="description" class="mt-2 text-sm leading-7 text-gray-500 dark:text-gray-400">
      {{ description }}
    </p>
    <div class="mt-4 space-y-4">
      <slot />
    </div>
  </section>
</template>

<script setup lang="ts">
defineProps<{
  title: string
  /** 锚点 id；省略则不渲染锚点链接 */
  anchor?: string
  description?: string
}>()
</script>
