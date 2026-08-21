<template>
  <!--
    帮助文档入口：新窗口打开独立的 /help 模块。
    用原生 <a target="_blank"> 而非 router-link，才能真正开新窗口且不影响当前控制台状态；
    href 经 router.resolve 生成，自动带上部署时的 BASE_URL。
  -->
  <a
    v-tooltip:bottom="'帮助文档（新窗口打开）'"
    :href="helpHref"
    target="_blank"
    rel="noopener noreferrer"
    class="hover:text-brand-600 dark:hover:text-brand-300 hover:border-brand-300 dark:hover:border-brand-500/40 relative flex h-11 w-11 items-center justify-center rounded-full border border-gray-200 bg-white text-gray-500 transition-colors hover:bg-gray-100 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400 dark:hover:bg-gray-800"
    aria-label="帮助文档，在新窗口打开"
  >
    <HelpCircleIcon class="h-5 w-5" aria-hidden="true" />
  </a>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { HELP_HOME_ROUTE } from '@/router/help'
import HelpCircleIcon from '@/icons/HelpCircleIcon.vue'

const router = useRouter()

const helpHref = computed(() => router.resolve({ name: HELP_HOME_ROUTE }).href)
</script>
