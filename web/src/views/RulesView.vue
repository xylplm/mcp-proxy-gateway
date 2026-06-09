<script setup lang="ts">
/**
 * 规则管理页（任务 26.2）。
 *
 * 覆盖 Req 8.1（别名/描述重写规则增删改、排序）、9.1（MCP 级屏蔽规则增删改、启停、排序）、
 * 17.5（管理 REST API 接入）。
 *
 * 布局：顶部上游 MCP 选择器；下方两个区块（别名规则 + MCP 级屏蔽规则）。
 * 响应式：借助 Tailwind 响应式工具类，大屏（lg/2xl）双栏并排提升信息密度，小屏堆叠。
 * 风格：Tailwind 工具类 + TailAdmin 组件风格（卡片、表格、表单、模态框、徽章、按钮）。
 */
import { onMounted, ref, watch } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import AliasRuleSection from '@/components/rules/AliasRuleSection.vue'
import FilterRuleSection from '@/components/rules/FilterRuleSection.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { listUpstreams, type Upstream } from '@/api/upstreams'

const { isLargeScreen } = useBreakpoint()

/** 全量上游列表（供选择器使用，按 sortOrder 升序）。 */
const upstreams = ref<Upstream[]>([])
/** 当前选中的上游 MCP 标识。 */
const selectedId = ref('')
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

/** 加载上游列表，默认选中首个。 */
async function loadUpstreams(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listUpstreams()
    list.sort((a, b) => a.config.sortOrder - b.config.sortOrder)
    upstreams.value = list
    if (selectedId.value === '' && list.length > 0) {
      selectedId.value = list[0].id
    }
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载上游列表失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadUpstreams)

// 若选中项被删除（不在列表中），回退到首个可用上游。
watch(upstreams, (list) => {
  if (selectedId.value !== '' && !list.some((u) => u.id === selectedId.value)) {
    selectedId.value = list.length > 0 ? list[0].id : ''
  }
})
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="规则管理" />

    <!-- 上游选择器工具栏 -->
    <div
      class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div class="min-w-[240px] flex-1">
          <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
            选择上游 MCP
          </label>
          <select
            v-model="selectedId"
            :disabled="loading || upstreams.length === 0"
            class="w-full max-w-md rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none disabled:opacity-60 dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
          >
            <option v-if="upstreams.length === 0" value="">暂无上游 MCP</option>
            <option v-for="up in upstreams" :key="up.id" :value="up.id">
              {{ up.config.name }}
            </option>
          </select>
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            规则绑定到所选上游 MCP，仅作用于其工具列表
          </p>
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="loading"
          @click="loadUpstreams"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path d="M4 4v6h6M20 20v-6h-6M20 9a8 8 0 0 0-15-2M4 15a8 8 0 0 0 15 2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          刷新上游列表
        </button>
      </div>

      <p
        v-if="errorMessage !== ''"
        class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ errorMessage }}
      </p>
    </div>

    <!-- 操作提示 -->
    <p
      v-if="toast !== ''"
      class="mb-4 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
    >
      {{ toast }}
    </p>

    <!--
      规则看板：
      - 小屏（默认）单列堆叠；
      - 大屏（lg 及以上）双栏并排，2xl 保持双栏，借助 Tailwind 网格提升信息密度。
      isLargeScreen 仅用于辅助标识，实际栏数由 Tailwind 响应式工具类驱动。
    -->
    <div
      class="grid grid-cols-1 gap-5 lg:grid-cols-2"
      :data-large-screen="isLargeScreen"
    >
      <AliasRuleSection :upstream-id="selectedId" @toast="showToast" />
      <FilterRuleSection :upstream-id="selectedId" @toast="showToast" />
    </div>

    <p
      v-if="!loading && upstreams.length === 0"
      class="mt-5 rounded-2xl border border-dashed border-gray-300 px-5 py-10 text-center text-sm text-gray-400 dark:border-gray-700"
    >
      暂无上游 MCP，请先在「上游 MCP 管理」中接入服务后再配置规则。
    </p>
  </AdminLayout>
</template>
