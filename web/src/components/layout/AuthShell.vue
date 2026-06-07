<script setup lang="ts">
/**
 * 鉴权页（登录 / 首次初始化）共用外壳。
 *
 * 设计要点（对应 design.md「鉴权中间件」与 Req 17.4 / 17.6）：
 * - 仅承载视觉外观（渐变背景、光晕、网格、卡片、Logo），不耦合任何登录/注册业务；
 * - 通过插槽 `default` 接收页面自有的表单与文案，由各页面自行组装；
 * - 视觉上与原 LoginView 保持一致，避免登录/注册页拆分后风格漂移。
 */
import FullScreenLayout from '@/components/layout/FullScreenLayout.vue'
</script>

<template>
  <FullScreenLayout>
    <!-- 浅色微妙渐变 + 远景柔和光晕 + 细网格，简洁大气、不空荡 -->
    <div
      class="relative flex min-h-screen w-full items-center justify-center overflow-hidden bg-gradient-to-br from-gray-50 via-white to-gray-100 p-6 dark:from-gray-950 dark:via-gray-900 dark:to-gray-950"
    >
      <div
        aria-hidden="true"
        class="pointer-events-none absolute -top-40 -left-40 h-96 w-96 rounded-full bg-brand-500/20 blur-3xl dark:bg-brand-500/15"
      ></div>
      <div
        aria-hidden="true"
        class="pointer-events-none absolute -right-40 -bottom-40 h-96 w-96 rounded-full bg-indigo-400/20 blur-3xl dark:bg-indigo-500/15"
      ></div>
      <div
        aria-hidden="true"
        class="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,rgba(70,95,255,0.06)_1px,transparent_1px),linear-gradient(to_bottom,rgba(70,95,255,0.06)_1px,transparent_1px)] bg-[size:48px_48px] [mask-image:radial-gradient(ellipse_at_center,black_40%,transparent_75%)] dark:bg-[linear-gradient(to_right,rgba(255,255,255,0.04)_1px,transparent_1px),linear-gradient(to_bottom,rgba(255,255,255,0.04)_1px,transparent_1px)]"
      ></div>

      <!-- 内容卡片：登录 / 注册页通过插槽注入各自的表单 -->
      <div
        class="relative w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl shadow-brand-500/5 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <!-- 头部：仅展示 logo，简洁大气 -->
        <div class="flex items-center justify-center px-8 pt-10 pb-2">
          <img src="/images/logo/auth-logo.svg" alt="MCP Gateway" class="h-10 w-auto" />
        </div>

        <div class="px-8 pt-6 pb-8">
          <slot />
        </div>
      </div>
    </div>
  </FullScreenLayout>
</template>
