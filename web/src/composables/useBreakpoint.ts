/**
 * 「当前断点」组合式函数（composable）。
 *
 * 监听窗口 resize 事件，集中计算并响应式地暴露当前视口断点、各类布尔标志，
 * 以及随断点切换的布局参数（侧边栏形态、表单列数、表格分页条数、主内容最大宽度）。
 * 侧边栏、表格列可见性、表单列数等均应通过本 composable 获取，避免各页面硬编码。
 *
 * 覆盖需求：17.3、17.7
 */
import { computed, onMounted, onUnmounted, readonly, ref, type Ref } from 'vue'
import {
  Breakpoint,
  type BreakpointKey,
  type SidebarModeKey,
  SidebarMode,
  CONTENT_MAX_WIDTH,
  FORM_COLUMNS_BY_BREAKPOINT,
  PAGE_SIZE_BY_BREAKPOINT,
  SIDEBAR_MODE_BY_BREAKPOINT,
  resolveBreakpoint,
} from '@/constants/breakpoints'

/** useBreakpoint 返回的响应式上下文。 */
export interface BreakpointContext {
  /** 当前视口宽度（px，只读响应式）。 */
  width: Readonly<Ref<number>>
  /** 当前断点枚举（只读响应式）。 */
  breakpoint: Readonly<Ref<BreakpointKey>>
  /** 是否手机档位（< 768px）。 */
  isMobile: Readonly<Ref<boolean>>
  /** 是否平板档位（768–1023px）。 */
  isTablet: Readonly<Ref<boolean>>
  /** 是否笔记本/PC 档位（1024–1439px）。 */
  isDesktop: Readonly<Ref<boolean>>
  /** 是否宽屏档位（1440–2559px）。 */
  isWide: Readonly<Ref<boolean>>
  /** 是否 4K 超大屏档位（≥ 2560px）。 */
  isUltraWide: Readonly<Ref<boolean>>
  /** 是否「小屏」（手机或平板），常用于切换抽屉/折叠等紧凑形态。 */
  isSmallScreen: Readonly<Ref<boolean>>
  /** 是否「大屏」（宽屏或 4K），即需要主内容最大宽度约束的档位（≥ 1440px）。 */
  isLargeScreen: Readonly<Ref<boolean>>
  /** 当前断点下侧边栏的默认形态。 */
  sidebarMode: Readonly<Ref<SidebarModeKey>>
  /** 当前断点下表单的默认列数。 */
  formColumns: Readonly<Ref<number>>
  /** 当前断点下表格的默认分页条数。 */
  pageSize: Readonly<Ref<number>>
  /**
   * 当前断点下主内容区的最大宽度（px）。
   * 大屏（≥ 1440px）返回约束值，其余档位返回 null 表示不限制（铺满）。
   */
  contentMaxWidth: Readonly<Ref<number | null>>
}

/**
 * 读取当前视口宽度。
 * 在非浏览器环境（SSR、单测无 window）下返回 0，由 resolveBreakpoint 兜底为手机档位。
 */
function readViewportWidth(): number {
  if (typeof window === 'undefined') {
    return 0
  }
  // 优先使用 documentElement.clientWidth，避免把滚动条宽度计入。
  return document.documentElement?.clientWidth || window.innerWidth || 0
}

/**
 * 创建并返回响应式断点上下文。
 *
 * 自动在组件挂载时注册 resize 监听、卸载时移除，避免内存泄漏。
 * 必须在组件 `setup` 作用域内调用，以便正确绑定生命周期钩子。
 */
export function useBreakpoint(): BreakpointContext {
  const width = ref<number>(readViewportWidth())

  // resize 处理：仅在宽度变化时更新，减少无谓的响应式触发。
  const handleResize = (): void => {
    const next = readViewportWidth()
    if (next !== width.value) {
      width.value = next
    }
  }

  onMounted(() => {
    // 挂载后立即同步一次，覆盖初始化到挂载之间可能发生的尺寸变化。
    handleResize()
    window.addEventListener('resize', handleResize, { passive: true })
  })

  onUnmounted(() => {
    window.removeEventListener('resize', handleResize)
  })

  const breakpoint = computed<BreakpointKey>(() => resolveBreakpoint(width.value))

  const isMobile = computed(() => breakpoint.value === Breakpoint.Mobile)
  const isTablet = computed(() => breakpoint.value === Breakpoint.Tablet)
  const isDesktop = computed(() => breakpoint.value === Breakpoint.Desktop)
  const isWide = computed(() => breakpoint.value === Breakpoint.Wide)
  const isUltraWide = computed(() => breakpoint.value === Breakpoint.UltraWide)

  const isSmallScreen = computed(() => isMobile.value || isTablet.value)
  const isLargeScreen = computed(() => isWide.value || isUltraWide.value)

  const sidebarMode = computed<SidebarModeKey>(
    () => SIDEBAR_MODE_BY_BREAKPOINT[breakpoint.value] ?? SidebarMode.Expanded,
  )
  const formColumns = computed<number>(() => FORM_COLUMNS_BY_BREAKPOINT[breakpoint.value] ?? 1)
  const pageSize = computed<number>(() => PAGE_SIZE_BY_BREAKPOINT[breakpoint.value] ?? 10)
  const contentMaxWidth = computed<number | null>(
    () => CONTENT_MAX_WIDTH[breakpoint.value] ?? null,
  )

  return {
    width: readonly(width),
    breakpoint: readonly(breakpoint),
    isMobile: readonly(isMobile),
    isTablet: readonly(isTablet),
    isDesktop: readonly(isDesktop),
    isWide: readonly(isWide),
    isUltraWide: readonly(isUltraWide),
    isSmallScreen: readonly(isSmallScreen),
    isLargeScreen: readonly(isLargeScreen),
    sidebarMode: readonly(sidebarMode),
    formColumns: readonly(formColumns),
    pageSize: readonly(pageSize),
    contentMaxWidth: readonly(contentMaxWidth),
  }
}
