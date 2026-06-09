<script setup lang="ts">
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { BarChartIcon, BoxCubeIcon, InfoCircleIcon, PlugInIcon, SettingsIcon } from '@/icons'

const githubURL = 'https://github.com/xylplm/mcp-proxy-gateway'
const dockerURL = 'https://hub.docker.com/r/xylplm/mcp-proxy-gateway'
const version = __APP_VERSION__

const highlights = [
  {
    title: '统一聚合',
    desc: '把多个上游 MCP 服务聚合成一个可管控的工具集合。',
    icon: PlugInIcon,
  },
  {
    title: '多端接入',
    desc: '对外提供 SSE、Streamable HTTP、WebSocket 等 MCP 访问方式。',
    icon: BoxCubeIcon,
  },
  {
    title: '可观测与审计',
    desc: '提供调用统计、错误排行、操作审计和健康状态。',
    icon: BarChartIcon,
  },
]

const techStack = [
  'Go 1.25',
  'Gin',
  'PostgreSQL',
  'Redis',
  'Vue 3',
  'Vite',
  'TypeScript',
  'Tailwind CSS',
]

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="关于" />

    <section :class="cardClass">
      <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.1fr)_minmax(320px,0.9fr)]">
        <div>
          <div
            class="inline-flex h-12 w-12 items-center justify-center rounded-xl bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
          >
            <InfoCircleIcon class="h-6 w-6" />
          </div>
          <h3 class="mt-5 text-xl font-semibold text-gray-800 dark:text-white/90">
            MCP Proxy Gateway
          </h3>
          <p class="mt-3 max-w-3xl text-sm leading-6 text-gray-600 dark:text-gray-300">
            一个面向 MCP 的聚合与代理网关，用于连接不同传输方式的上游 MCP 服务，统一暴露给外部 AI 客户端，并提供 API Key、规则、统计、审计和小智接入等管理能力。
          </p>
          <div class="mt-6 flex flex-wrap gap-3">
            <a
              :href="githubURL"
              target="_blank"
              rel="noreferrer"
              class="inline-flex items-center justify-center rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600"
            >
              查看 GitHub
            </a>
            <a
              :href="dockerURL"
              target="_blank"
              rel="noreferrer"
              class="inline-flex items-center justify-center rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
            >
              Docker 镜像
            </a>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-1">
          <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <p class="text-xs text-gray-400 dark:text-gray-500">项目版本</p>
            <p class="mt-1 text-lg font-semibold text-gray-800 dark:text-white/90">v{{ version }}</p>
          </div>
          <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <p class="text-xs text-gray-400 dark:text-gray-500">代码仓库</p>
            <p class="mt-1 break-all text-sm font-medium text-gray-800 dark:text-white/90">
              {{ githubURL }}
            </p>
          </div>
        </div>
      </div>
    </section>

    <section class="mt-6 grid grid-cols-1 gap-5 lg:grid-cols-3">
      <article
        v-for="item in highlights"
        :key="item.title"
        :class="cardClass"
        class="min-h-44"
      >
        <span
          class="flex h-11 w-11 items-center justify-center rounded-xl bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400"
        >
          <component :is="item.icon" class="h-5 w-5" />
        </span>
        <h4 class="mt-4 text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.title }}</h4>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ item.desc }}</p>
      </article>
    </section>

    <section :class="cardClass" class="mt-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">技术栈</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            后端、前端与基础设施组件概览。
          </p>
        </div>
        <SettingsIcon class="h-5 w-5 text-gray-400" />
      </div>
      <div class="mt-5 flex flex-wrap gap-2">
        <span
          v-for="item in techStack"
          :key="item"
          class="rounded-full bg-gray-100 px-3 py-1.5 text-xs font-medium text-gray-700 dark:bg-white/5 dark:text-gray-300"
        >
          {{ item }}
        </span>
      </div>
    </section>
  </AdminLayout>
</template>
