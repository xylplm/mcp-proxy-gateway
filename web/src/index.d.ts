import type Tooltip from './components/common/Tooltip.vue'
import type { TooltipDirectiveValue } from './directives/tooltip'

export {}

declare module 'vue' {
  export interface GlobalComponents {
    Tooltip: typeof Tooltip
  }

  export interface GlobalDirectives {
    tooltip: TooltipDirectiveValue
  }
}
