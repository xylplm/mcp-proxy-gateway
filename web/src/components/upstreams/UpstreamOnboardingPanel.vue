<script setup lang="ts">
import { RouterLink } from 'vue-router'
import ConnStateBadge from '@/components/upstreams/ConnStateBadge.vue'
import { RefreshIcon } from '@/icons'
import type { Upstream } from '@/api/upstreams'
import { toolCountLabel, type ToolCountSnapshot } from '@/utils/toolCountSnapshot'

defineProps<{
  upstream: Upstream
  toolCount?: ToolCountSnapshot
  refreshing: boolean
  reconnecting: boolean
}>()

defineEmits<{
  (e: 'refresh'): void
  (e: 'reconnect'): void
  (e: 'open-tools'): void
  (e: 'dismiss'): void
}>()
</script>

<template>
  <section class="mb-5 rounded-2xl border border-brand-200 bg-brand-50 p-4 dark:border-brand-500/30 dark:bg-brand-500/10">
    <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">
            {{ upstream.config.name }} 已创建
          </h3>
          <ConnStateBadge :state="upstream.state" />
          <span class="rounded-full bg-white/70 px-2 py-0.5 text-xs text-gray-600 dark:bg-gray-900/50 dark:text-gray-300">
            {{ toolCountLabel(toolCount) }}
          </span>
        </div>
        <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">
          建议先触发连接并同步工具，确认工具可见后再复制客户端配置。
        </p>
      </div>
      <button
        type="button"
        class="self-start rounded-lg px-2 py-1 text-sm text-gray-500 transition hover:bg-white/70 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-900/40 dark:hover:text-gray-200"
        @click="$emit('dismiss')"
      >
        收起
      </button>
    </div>

    <div class="mt-4 grid grid-cols-1 gap-2 md:grid-cols-4">
      <button
        type="button"
        class="rounded-lg border border-brand-200 bg-white px-3 py-2 text-left text-sm font-medium text-brand-700 transition hover:bg-brand-50 disabled:opacity-60 dark:border-brand-500/30 dark:bg-gray-900/50 dark:text-brand-300 dark:hover:bg-brand-500/10"
        :disabled="reconnecting"
        @click="$emit('reconnect')"
      >
        <span class="flex items-center gap-2">
          <RefreshIcon class="h-4 w-4" :class="{ 'animate-spin': reconnecting }" />
          {{ reconnecting ? '连接中...' : '触发连接' }}
        </span>
        <span class="mt-1 block text-xs font-normal text-gray-500 dark:text-gray-400">让网关立即尝试连接该上游</span>
      </button>

      <button
        type="button"
        class="rounded-lg border border-brand-200 bg-white px-3 py-2 text-left text-sm font-medium text-brand-700 transition hover:bg-brand-50 disabled:opacity-60 dark:border-brand-500/30 dark:bg-gray-900/50 dark:text-brand-300 dark:hover:bg-brand-500/10"
        :disabled="refreshing"
        @click="$emit('refresh')"
      >
        <span class="flex items-center gap-2">
          <RefreshIcon class="h-4 w-4" :class="{ 'animate-spin': refreshing }" />
          {{ refreshing ? '同步中...' : '同步工具' }}
        </span>
        <span class="mt-1 block text-xs font-normal text-gray-500 dark:text-gray-400">拉取工具列表并更新缓存</span>
      </button>

      <button
        type="button"
        class="rounded-lg border border-brand-200 bg-white px-3 py-2 text-left text-sm font-medium text-brand-700 transition hover:bg-brand-50 dark:border-brand-500/30 dark:bg-gray-900/50 dark:text-brand-300 dark:hover:bg-brand-500/10"
        @click="$emit('open-tools')"
      >
        查看工具
        <span class="mt-1 block text-xs font-normal text-gray-500 dark:text-gray-400">检查该上游已经同步的工具</span>
      </button>

      <RouterLink
        :to="{ name: 'Tools', query: { upstreamId: upstream.id } }"
        class="rounded-lg border border-brand-200 bg-white px-3 py-2 text-left text-sm font-medium text-brand-700 transition hover:bg-brand-50 dark:border-brand-500/30 dark:bg-gray-900/50 dark:text-brand-300 dark:hover:bg-brand-500/10"
      >
        进入工具目录
        <span class="mt-1 block text-xs font-normal text-gray-500 dark:text-gray-400">查看聚合后的对外工具</span>
      </RouterLink>
    </div>
  </section>
</template>
