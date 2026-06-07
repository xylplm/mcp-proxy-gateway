<template>
  <div
    class="relative z-1 flex min-h-screen flex-col items-center justify-center overflow-hidden p-6"
  >
    <common-grid-shape />
    <!-- Centered Content -->
    <div class="mx-auto w-full max-w-[242px] text-center sm:max-w-[472px]">
      <h1 class="text-title-md xl:text-title-2xl mb-8 font-bold text-gray-800 dark:text-white/90">
        ERROR
      </h1>

      <img src="/images/error/404.svg" alt="404" class="dark:hidden" />
      <img src="/images/error/404-dark.svg" alt="404" class="hidden dark:block" />

      <p class="mt-10 mb-6 text-base text-gray-700 sm:text-lg dark:text-gray-400">
        抱歉，您访问的页面不存在。
      </p>

      <router-link
        to="/"
        class="shadow-theme-xs inline-flex items-center justify-center rounded-lg border border-gray-300 bg-white px-5 py-3.5 text-sm font-medium text-gray-700 hover:bg-gray-50 hover:text-gray-800 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-white/[0.03] dark:hover:text-gray-200"
      >
        返回首页
      </router-link>
    </div>
    <!-- Footer -->
    <p
      class="absolute bottom-6 left-1/2 -translate-x-1/2 text-center text-sm text-gray-500 dark:text-gray-400"
    >
      &copy; {{ currentYear }} - MCP Proxy Gateway
    </p>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import CommonGridShape from '../../components/common/CommonGridShape.vue'

const currentYear = ref(new Date().getFullYear())
</script>
