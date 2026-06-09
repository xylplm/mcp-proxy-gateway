<script setup lang="ts">
/**
 * 规则管理页（任务 26.2）。
 *
 * 覆盖 Req 8.1（别名/描述重写规则增删改、排序）、9.1（MCP 级屏蔽规则增删改、启停、排序）、
 * 17.5（管理 REST API 接入）。
 *
 * 布局：规则中心，规则独立创建，作用范围支持全部上游或指定多个上游。
 * 响应式：卡片与网格布局，随可用空间调整信息密度，避免表格。
 */
import { onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import AliasRuleSection from '@/components/rules/AliasRuleSection.vue'
import FilterRuleSection from '@/components/rules/FilterRuleSection.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { listUpstreams, type Upstream } from '@/api/upstreams'

const { isLargeScreen } = useBreakpoint()

/** 全量上游列表（供选择器使用，按 sortOrder 升序）。 */
const upstreams = ref<Upstream[]>([])
const aliasSection = ref<InstanceType<typeof AliasRuleSection> | null>(null)
const filterSection = ref<InstanceType<typeof FilterRuleSection> | null>(null)
/** 上游列表加载/错误状态。 */
const loading = ref(false)
const errorMessage = ref('')
/** 操作结果提示。 */
const toast = ref('')

/** 展示短暂提示。 */
function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

/** 加载上游列表，供规则作用范围多选使用。 */
async function loadUpstreams(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listUpstreams()
    list.sort((a, b) => a.config.sortOrder - b.config.sortOrder)
    upstreams.value = list
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载上游列表失败'
  } finally {
    loading.value = false
  }
}

async function refreshAll(): Promise<void> {
  await loadUpstreams()
  await Promise.all([aliasSection.value?.reload(), filterSection.value?.reload()])
}

onMounted(loadUpstreams)
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="规则管理" />

    <!-- 规则中心说明 -->
    <div
      class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <div>
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">规则中心</h2>
          <button
            v-tooltip:bottom-end="'刷新'"
            type="button"
            class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-gray-300 text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="loading"
            aria-label="刷新"
            @click="refreshAll"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              :class="{ 'animate-spin': loading }"
              aria-hidden="true"
            >
              <path
                d="M4 4v6h6M20 20v-6h-6M20 9a8 8 0 0 0-15-2M4 15a8 8 0 0 0 15 2"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
        </div>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          规则独立创建，再选择作用范围：可应用到全部上游，也可只应用到指定上游。
        </p>
        <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">可用上游</div>
            <div class="mt-1 text-xl font-semibold text-gray-800 dark:text-white/90">
              {{ upstreams.length }}
            </div>
          </div>
          <div class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">作用范围</div>
            <div class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              全部 / 多选上游
            </div>
          </div>
          <div class="rounded-xl bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">规则类型</div>
            <div class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              别名重写 / 过滤
            </div>
          </div>
        </div>
      </div>

      <p
        v-if="errorMessage !== ''"
        class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mt-4 rounded-lg px-4 py-2.5 text-sm"
      >
        {{ errorMessage }}
      </p>
    </div>

    <!-- 操作提示 -->
    <p
      v-if="toast !== ''"
      class="bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
    >
      {{ toast }}
    </p>

    <!-- 规则看板：两个规则区块按可用空间自动排列，内部卡片继续自适应。 -->
    <div class="grid grid-cols-1 gap-5 lg:grid-cols-2" :data-large-screen="isLargeScreen">
      <AliasRuleSection ref="aliasSection" :upstreams="upstreams" @toast="showToast" />
      <FilterRuleSection ref="filterSection" :upstreams="upstreams" @toast="showToast" />
    </div>
  </AdminLayout>
</template>
