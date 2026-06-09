<script setup lang="ts">
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  BarChartIcon,
  BoxCubeIcon,
  DocsIcon,
  InfoCircleIcon,
  PlugInIcon,
  SettingsIcon,
  UserGroupIcon,
} from '@/icons'

const githubURL = 'https://github.com/xylplm/mcp-proxy-gateway'
const dockerURL = 'https://hub.docker.com/r/xylplm/mcp-proxy-gateway'
const version = __APP_VERSION__

const capabilities = [
  { title: '统一聚合', desc: '将多个上游 MCP 服务合并成一个可管控的工具出口。', icon: PlugInIcon },
  { title: '访问治理', desc: '通过 API Key、来源白名单、限流和规则控制工具可见性。', icon: UserGroupIcon },
  { title: '多协议暴露', desc: '对外提供 SSE、Streamable HTTP 与 WebSocket 接入地址。', icon: BoxCubeIcon },
  { title: '可观测', desc: '提供调用统计、调用记录、错误排行、健康状态与审计日志。', icon: BarChartIcon },
]

const facts = [
  { label: '项目版本', value: `v${version}` },
  { label: '前端管理台', value: 'Vue 3 / Vite / TypeScript / Tailwind CSS' },
  { label: '后端服务', value: 'Go / Gin / PostgreSQL / Redis' },
  { label: '对外协议', value: 'SSE / Streamable HTTP / WebSocket' },
]

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="关于" />

    <section
      class="overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <div class="grid grid-cols-1 gap-0 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <div class="p-6 md:p-8">
          <div
            class="inline-flex h-14 w-14 items-center justify-center rounded-2xl bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
          >
            <InfoCircleIcon class="h-7 w-7" />
          </div>
          <h2 class="mt-6 text-2xl font-semibold text-gray-900 dark:text-white">
            MCP Proxy Gateway
          </h2>
          <p class="mt-4 max-w-4xl text-sm leading-7 text-gray-600 dark:text-gray-300">
            面向 MCP 的聚合代理网关，负责连接不同传输方式的上游 MCP 服务，将工具统一暴露给外部 AI 客户端，并在管理台中提供接入、鉴权、规则、统计、审计与第三方对接能力。
          </p>
          <div class="mt-6 flex flex-wrap gap-3">
            <a
              :href="githubURL"
              target="_blank"
              rel="noreferrer"
              class="inline-flex items-center justify-center gap-2 rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600"
            >
              <DocsIcon class="h-4 w-4" />
              GitHub 仓库
            </a>
            <a
              :href="dockerURL"
              target="_blank"
              rel="noreferrer"
              class="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
            >
              <BoxCubeIcon class="h-4 w-4" />
              Docker 镜像
            </a>
          </div>
        </div>

        <div class="border-t border-gray-200 bg-gray-50 p-6 xl:border-t-0 xl:border-l dark:border-gray-800 dark:bg-gray-900/40">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-1">
            <div
              v-for="item in facts"
              :key="item.label"
              class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
            >
              <p class="text-xs text-gray-400 dark:text-gray-500">{{ item.label }}</p>
              <p class="mt-1 text-sm font-semibold leading-6 text-gray-800 dark:text-white/90">
                {{ item.value }}
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>

    <section class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <article v-for="item in capabilities" :key="item.title" :class="cardClass" class="min-h-44">
        <span
          class="flex h-11 w-11 items-center justify-center rounded-xl bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400"
        >
          <component :is="item.icon" class="h-5 w-5" />
        </span>
        <h3 class="mt-4 text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.title }}</h3>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ item.desc }}</p>
      </article>
    </section>

    <section class="mt-6 grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
      <div :class="cardClass">
        <div class="flex items-start justify-between gap-4">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">代码仓库</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">项目源码、发布与问题跟踪入口。</p>
          </div>
          <SettingsIcon class="h-5 w-5 text-gray-400" />
        </div>
        <p class="mt-5 break-all rounded-xl bg-gray-50 p-4 font-mono text-xs leading-6 text-gray-700 dark:bg-gray-900/60 dark:text-gray-200">
          {{ githubURL }}
        </p>
      </div>

      <div :class="cardClass">
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">维护信息</h3>
        <div class="mt-5 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">配置来源</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">YAML + 管理 API</p>
          </div>
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">数据保留</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">统计 / 审计可配置</p>
          </div>
          <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
            <p class="text-xs text-gray-400 dark:text-gray-500">运行定位</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">自部署网关</p>
          </div>
        </div>
      </div>
    </section>
  </AdminLayout>
</template>
