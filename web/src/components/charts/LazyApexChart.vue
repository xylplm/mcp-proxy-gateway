<script setup lang="ts">
/**
 * 懒加载 ApexCharts 封装：
 * - 仅在进入视口后动态 import vue3-apexcharts / apexcharts
 * - 避免仪表盘等页面首包吞下 500KB+ 图表库
 * - 卸载时释放组件引用，降低长会话内存占用
 */
import { markRaw, onMounted, onUnmounted, ref, shallowRef } from 'vue'

const props = withDefaults(
  defineProps<{
    type: string
    series: unknown
    options?: object
    height?: number | string
    width?: number | string
  }>(),
  {
    options: () => ({}),
    height: 320,
    width: '100%',
  },
)

const hostRef = ref<HTMLElement | null>(null)
const shouldLoad = ref(false)
// 用 any 规避 vue3-apexcharts 组件类型与 shallowRef 泛型在 vue-tsc 下的冲突。
const ChartComp = shallowRef<any>(null)
let observer: IntersectionObserver | null = null
let loadPromise: Promise<void> | null = null

async function loadChart(): Promise<void> {
  if (ChartComp.value != null || loadPromise != null) {
    if (loadPromise != null) await loadPromise
    return
  }
  loadPromise = import('vue3-apexcharts').then((mod) => {
    const comp = (mod as { default?: unknown }).default ?? mod
    ChartComp.value = markRaw(comp as object)
  })
  await loadPromise
}

function setupObserver(): void {
  if (shouldLoad.value) {
    void loadChart()
    return
  }
  if (typeof IntersectionObserver === 'undefined') {
    shouldLoad.value = true
    void loadChart()
    return
  }
  observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((item) => item.isIntersecting)) {
        shouldLoad.value = true
        void loadChart()
        observer?.disconnect()
        observer = null
      }
    },
    { rootMargin: '200px 0px' },
  )
  if (hostRef.value) observer.observe(hostRef.value)
}

onMounted(setupObserver)

onUnmounted(() => {
  observer?.disconnect()
  observer = null
  ChartComp.value = null
  loadPromise = null
})
</script>

<template>
  <div ref="hostRef" class="min-h-[200px] w-full">
    <component
      :is="ChartComp"
      v-if="ChartComp && shouldLoad"
      :type="props.type"
      :height="props.height"
      :width="props.width"
      :options="props.options"
      :series="props.series"
    />
    <div
      v-else
      class="flex h-full min-h-[200px] items-center justify-center rounded-xl bg-gradient-to-br from-gray-50 to-gray-100/80 text-xs text-gray-400 dark:from-white/[0.03] dark:to-white/[0.05] dark:text-gray-500"
      :style="{ height: typeof props.height === 'number' ? `${props.height}px` : props.height }"
    >
      <span class="inline-flex items-center gap-2">
        <span
          class="border-brand-400/70 h-3.5 w-3.5 animate-spin rounded-full border-2 border-t-transparent"
          aria-hidden="true"
        ></span>
        图表加载中…
      </span>
    </div>
  </div>
</template>
