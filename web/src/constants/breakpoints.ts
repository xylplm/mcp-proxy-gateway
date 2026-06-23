/**
 * 全局响应式断点常量。
 *
 * 与设计文档「响应式布局策略」五档断点表保持一致，供「当前断点」composable、
 * Tailwind 响应式工具类与 CSS 媒体查询共用，避免各页面各自硬编码。
 *
 * 与 `src/assets/main.css` 中 Tailwind v4 `@theme` 自定义断点对齐：
 * - `md`   = 768px（平板起始）
 * - `lg`   = 1024px（笔记本/PC 起始）
 * - `wide` = 1440px（宽屏起始，自定义断点）
 * - `4k`   = 2560px（4K 超大屏起始，自定义断点）
 *
 * 覆盖需求：17.3、17.7
 */

/**
 * 五档断点枚举。
 * - `mobile`    手机：宽度 < 768px
 * - `tablet`    平板：宽度 768–1023px
 * - `desktop`   笔记本/PC：宽度 1024–1439px
 * - `wide`      宽屏：宽度 1440–2559px
 * - `ultraWide` 4K 超大屏：宽度 ≥ 2560px
 */
export const Breakpoint = {
  /** 手机：< 768px */
  Mobile: 'mobile',
  /** 平板：768–1023px */
  Tablet: 'tablet',
  /** 笔记本/PC：1024–1439px */
  Desktop: 'desktop',
  /** 宽屏：1440–2559px */
  Wide: 'wide',
  /** 4K 超大屏：≥ 2560px */
  UltraWide: 'ultraWide',
} as const

/** 断点枚举值类型。 */
export type BreakpointKey = (typeof Breakpoint)[keyof typeof Breakpoint]

/**
 * 各档位的「最小起始宽度」（单位 px）。
 * 即：视口宽度 ≥ 该值且 < 下一档起始值时，归属当前档位。
 * 这些数值与 Tailwind `@theme` 中 `md`/`lg`/`wide`/`4k` 断点一一对应。
 */
export const BREAKPOINT_MIN_WIDTH: Record<BreakpointKey, number> = {
  [Breakpoint.Mobile]: 0,
  [Breakpoint.Tablet]: 768,
  [Breakpoint.Desktop]: 1024,
  [Breakpoint.Wide]: 1440,
  [Breakpoint.UltraWide]: 2560,
}

/**
 * 断点判定顺序（从大到小），用于根据视口宽度匹配当前档位。
 * 从最大档位开始向下匹配，命中第一个「宽度 ≥ 起始值」的档位即为当前断点。
 */
export const BREAKPOINT_ORDER_DESC: BreakpointKey[] = [
  Breakpoint.UltraWide,
  Breakpoint.Wide,
  Breakpoint.Desktop,
  Breakpoint.Tablet,
  Breakpoint.Mobile,
]

/**
 * 主内容区在大屏（≥ 1440px）下的最大宽度约束（单位 px）。
 *
 * 需求 17.7：视口宽度不小于 1440px 时，主内容区限制在最大宽度内并居中，
 * 避免内容在宽屏与 4K 大屏下无限横向拉伸。
 *
 * - 宽屏（1440–2559px）：容器最大宽度 1600px。
 * - 4K 超大屏（≥ 2560px）：容器维持最大宽度居中（取更大值 2048px），不无限拉伸。
 */
export const CONTENT_MAX_WIDTH: Partial<Record<BreakpointKey, number>> = {
  [Breakpoint.Wide]: 1600,
  [Breakpoint.UltraWide]: 2048,
}

/** 启用主内容区最大宽度约束的起始视口宽度（单位 px）。 */
export const CONTENT_MAX_WIDTH_FROM = BREAKPOINT_MIN_WIDTH[Breakpoint.Wide]

/**
 * 各断点下表格组件的默认分页条数。
 * 大屏提高条数减少翻页，窄屏降低条数避免过长滚动。
 */
export const PAGE_SIZE_BY_BREAKPOINT: Record<BreakpointKey, number> = {
  [Breakpoint.Mobile]: 10,
  [Breakpoint.Tablet]: 10,
  [Breakpoint.Desktop]: 20,
  [Breakpoint.Wide]: 50,
  [Breakpoint.UltraWide]: 50,
}

/**
 * 各断点下表单的默认列数。
 * 窄屏单列保证可读与可触达，大屏多列提升信息密度。
 */
export const FORM_COLUMNS_BY_BREAKPOINT: Record<BreakpointKey, number> = {
  [Breakpoint.Mobile]: 1,
  [Breakpoint.Tablet]: 1,
  [Breakpoint.Desktop]: 2,
  [Breakpoint.Wide]: 2,
  [Breakpoint.UltraWide]: 3,
}

/**
 * 侧边栏在各断点下的展现形态。
 * - `drawer`    抽屉式（汉堡菜单触发覆盖层），用于手机和平板。
 * - `collapsed` 折叠为图标栏常驻，保留给后续更大屏紧凑形态。
 * - `expanded`  完整展开常驻，用于 PC 及以上。
 */
export const SidebarMode = {
  Drawer: 'drawer',
  Collapsed: 'collapsed',
  Expanded: 'expanded',
} as const

/** 侧边栏形态类型。 */
export type SidebarModeKey = (typeof SidebarMode)[keyof typeof SidebarMode]

/** 各断点下侧边栏的默认形态。 */
export const SIDEBAR_MODE_BY_BREAKPOINT: Record<BreakpointKey, SidebarModeKey> = {
  [Breakpoint.Mobile]: SidebarMode.Drawer,
  [Breakpoint.Tablet]: SidebarMode.Drawer,
  [Breakpoint.Desktop]: SidebarMode.Expanded,
  [Breakpoint.Wide]: SidebarMode.Expanded,
  [Breakpoint.UltraWide]: SidebarMode.Expanded,
}

/** 侧边栏展开态宽度（单位 px，与 TailAdmin AppSidebar 的 290px 对齐）。 */
export const SIDEBAR_WIDTH_EXPANDED = 290

/** 侧边栏折叠态（图标栏）宽度（单位 px，与 TailAdmin AppSidebar 的 90px 对齐）。 */
export const SIDEBAR_WIDTH_COLLAPSED = 90

/** 是否应使用抽屉式侧边栏（手机和平板）。 */
export function shouldUseSidebarDrawer(width: number): boolean {
  return width < BREAKPOINT_MIN_WIDTH[Breakpoint.Desktop]
}

/**
 * 根据视口宽度计算当前断点。
 *
 * @param width 视口宽度（单位 px）。
 * @returns 命中的断点枚举值；非正数或异常输入归为手机档位。
 */
export function resolveBreakpoint(width: number): BreakpointKey {
  // 从最大档位向下匹配，命中第一个满足「宽度 ≥ 起始值」的档位。
  for (const key of BREAKPOINT_ORDER_DESC) {
    if (width >= BREAKPOINT_MIN_WIDTH[key]) {
      return key
    }
  }
  // 兜底：任何异常宽度都归为手机档位（最保守、最可用）。
  return Breakpoint.Mobile
}
