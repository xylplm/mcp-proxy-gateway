<script setup lang="ts">
import type { Component } from 'vue'
import { ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { ArchiveIcon, BarChartIcon, BoxCubeIcon, DocsIcon, InfoCircleIcon, PlugInIcon, TaskIcon, UserGroupIcon } from '@/icons'
import { exportDiagnostics, exportGoroutineLeaks } from '@/api/diagnostics'
import { useToast } from '@/composables/useToast'

const githubURL = 'https://github.com/xylplm/mcp-proxy-gateway'
const dockerURL = 'https://hub.docker.com/r/xylplm/mcp-proxy-gateway'
const version = __APP_VERSION__
const toast = useToast()
const exportingDiagnostics = ref(false)
const exportingLeaks = ref(false)

const capabilities: ReadonlyArray<{ title: string; desc: string; icon: Component }> = [
  {
    title: '聚合工具',
    desc: '统一接入多个上游 MCP，将真实工具整理成稳定的对外能力。',
    icon: PlugInIcon,
  },
  {
    title: '治理访问',
    desc: '用 API Key、规则与限流控制谁能访问、能看到哪些工具。',
    icon: UserGroupIcon,
  },
  {
    title: '观测调用',
    desc: '通过统计、记录、审计与系统日志追踪网关运行状态。',
    icon: BarChartIcon,
  },
]

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'

/** 生成 yyyyMMdd-HHmmss 形式的本地时间戳，用于导出文件名。 */
function timestampNow(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
}

/** 触发浏览器下载给定 Blob。 */
function triggerBlobDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

async function downloadDiagnostics(): Promise<void> {
  if (exportingDiagnostics.value) return
  exportingDiagnostics.value = true
  try {
    const blob = await exportDiagnostics()
    triggerBlobDownload(blob, `mpg-diagnostics-${timestampNow()}.json`)
    toast.success('诊断包已导出')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导出诊断包失败')
  } finally {
    exportingDiagnostics.value = false
  }
}

async function downloadGoroutineLeaks(): Promise<void> {
  if (exportingLeaks.value) return
  exportingLeaks.value = true
  try {
    const blob = await exportGoroutineLeaks()
    triggerBlobDownload(blob, `mpg-goroutine-leaks-${timestampNow()}.txt`)
    toast.success('协程泄漏报告已导出')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导出协程泄漏报告失败')
  } finally {
    exportingLeaks.value = false
  }
}
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="关于" />

    <section :class="cardClass" class="overflow-hidden">
      <div class="flex flex-col gap-8 xl:flex-row xl:items-end xl:justify-between">
        <div class="max-w-4xl">
          <div
            class="inline-flex h-14 w-14 items-center justify-center rounded-2xl bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
          >
            <InfoCircleIcon class="h-7 w-7" />
          </div>
          <div class="mt-6 flex flex-wrap items-center gap-3">
            <h2 class="text-2xl font-semibold text-gray-900 dark:text-white">MCP Proxy Gateway</h2>
            <span
              class="rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 dark:bg-white/5 dark:text-gray-300"
            >
              v{{ version }}
            </span>
          </div>
          <p class="mt-4 text-sm leading-7 text-gray-600 dark:text-gray-300">
            一个面向自部署场景的 MCP 聚合网关。它负责连接上游 MCP 服务，统一向外部 AI 客户端提供稳定、可治理、可观测的工具出口。
          </p>
        </div>

        <div class="flex flex-wrap gap-3 xl:justify-end">
          <button
            type="button"
            class="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
            :disabled="exportingDiagnostics"
            @click="downloadDiagnostics"
          >
            <ArchiveIcon class="h-4 w-4" />
            {{ exportingDiagnostics ? '导出中...' : '下载诊断包' }}
          </button>
          <button
            v-tooltip:bottom-end="
              '导出已无法被唤醒、永久阻塞的协程及其调用栈，用于排查连接或后台任务未收尾的问题。采集会触发一次垃圾回收，耗时可达数十秒，请仅在排查时使用。'
            "
            type="button"
            class="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
            :disabled="exportingLeaks"
            @click="downloadGoroutineLeaks"
          >
            <TaskIcon class="h-4 w-4" />
            {{ exportingLeaks ? '采集中...' : '协程泄漏报告' }}
          </button>
          <a
            :href="githubURL"
            target="_blank"
            rel="noreferrer"
            class="inline-flex items-center justify-center gap-2 rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600"
          >
            <DocsIcon class="h-4 w-4" />
            GitHub
          </a>
          <a
            :href="dockerURL"
            target="_blank"
            rel="noreferrer"
            class="inline-flex items-center justify-center gap-2 rounded-lg border border-gray-200 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
          >
            <BoxCubeIcon class="h-4 w-4" />
            Docker
          </a>
        </div>
      </div>
    </section>

    <section class="mt-6 grid grid-cols-1 gap-4 md:grid-cols-3">
      <article v-for="item in capabilities" :key="item.title" :class="cardClass">
        <span
          class="flex h-11 w-11 items-center justify-center rounded-xl bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400"
        >
          <component :is="item.icon" class="h-5 w-5" />
        </span>
        <h3 class="mt-4 text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.title }}</h3>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ item.desc }}</p>
      </article>
    </section>
  </AdminLayout>
</template>
