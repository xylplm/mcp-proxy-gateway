<template>
  <!-- 文档页外壳：统一卡片容器、标题区与小节间距，避免各篇文档重复同一套结构。 -->
  <article
    class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm sm:p-7 lg:p-8 dark:border-gray-800 dark:bg-white/[0.03]"
  >
    <header class="border-b border-gray-100 pb-5 dark:border-white/5">
      <h1 class="text-xl font-semibold text-gray-800 sm:text-2xl dark:text-white/90">
        {{ title }}
      </h1>
      <p v-if="subtitle" class="mt-2 max-w-3xl text-sm leading-7 text-gray-500 dark:text-gray-400">
        {{ subtitle }}
      </p>
      <!-- 新窗口打开：帮助本身通常已是独立窗口，不该被这个跳转顶掉。 -->
      <a
        v-if="consoleHref"
        :href="consoleHref"
        target="_blank"
        rel="noopener noreferrer"
        class="text-brand-600 dark:text-brand-300 mt-3 inline-flex items-center gap-1 text-sm font-medium hover:underline"
      >
        打开{{ consoleLabel ?? '对应页面' }}
        <ChevronRightIcon class="h-4 w-4" aria-hidden="true" />
      </a>
    </header>
    <div class="mt-6 space-y-8 sm:mt-8 sm:space-y-10">
      <slot />
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ChevronRightIcon } from '@/icons'

const props = defineProps<{
  title: string
  subtitle?: string
  /** 对应的控制台页面路径；给出后渲染一个直达链接 */
  consolePath?: string
  consoleLabel?: string
}>()

const router = useRouter()

// 经 router.resolve 生成，自动带上部署时的 BASE_URL。
const consoleHref = computed(() =>
  props.consolePath ? router.resolve(props.consolePath).href : '',
)
</script>
