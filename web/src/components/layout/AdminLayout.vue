<template>
  <div class="min-h-screen xl:flex">
    <app-sidebar />
    <AppBackdrop />
    <div
      class="flex-1 transition-all duration-300 ease-in-out"
      :class="[isExpanded || isHovered ? 'lg:ml-[290px]' : 'lg:ml-[90px]']"
    >
      <app-header />
      <!--
        主内容容器：≥1440px（宽屏/4K）时限制最大宽度并居中（mx-auto），避免内容无限横向拉伸（需求 17.7）。
        最大宽度取自统一的「当前断点」composable（宽屏 1600px、4K 2048px），其余档位 contentMaxWidth 为 null，
        此时不设上限以铺满可用空间（笔记本/PC 及以下档位）。
      -->
      <div
        class="mx-auto w-full p-4 md:p-6"
        :style="contentMaxWidth !== null ? { maxWidth: `${contentMaxWidth}px` } : undefined"
      >
        <slot></slot>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'
import { useSidebar } from '@/composables/useSidebar'
import { useBreakpoint } from '@/composables/useBreakpoint'
import AppBackdrop from './AppBackdrop.vue'
const { isExpanded, isHovered } = useSidebar()
// 主内容区最大宽度约束随断点切换：≥1440px 返回约束值，其余档位为 null（铺满）。
const { contentMaxWidth } = useBreakpoint()
</script>
