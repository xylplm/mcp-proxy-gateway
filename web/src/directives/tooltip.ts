import type { Directive, DirectiveBinding } from 'vue'

export type TooltipPlacement =
  | 'top'
  | 'top-start'
  | 'top-end'
  | 'right'
  | 'right-start'
  | 'right-end'
  | 'bottom'
  | 'bottom-start'
  | 'bottom-end'
  | 'left'
  | 'left-start'
  | 'left-end'

export interface TooltipOptions {
  content: string
  placement?: TooltipPlacement
  disabled?: boolean
  /** 是否换行展示长文本（用于工具描述等长内容）；默认 false 保持单行截断。 */
  wrap?: boolean
}

export type TooltipDirectiveValue = string | TooltipOptions | null | undefined

type NormalizedTooltipOptions = Required<TooltipOptions>

type TooltipBasePlacement = 'top' | 'right' | 'bottom' | 'left'
type TooltipAlignment = 'start' | 'end' | undefined

interface TooltipState {
  id: string
  options: NormalizedTooltipOptions
  tooltip: HTMLSpanElement | null
  show: () => void
  hide: () => void
  updatePosition: () => void
  cleanupElement: () => void
  cleanupGlobal: (() => void) | null
  rafId: number | null
  hideTimer: number | null
}

const tooltipStates = new WeakMap<HTMLElement, TooltipState>()
let tooltipIdSeed = 0

const DEFAULT_OPTIONS: NormalizedTooltipOptions = {
  content: '',
  placement: 'top',
  disabled: false,
  wrap: true,
}

function normalizeOptions(
  value: TooltipDirectiveValue,
  arg?: string,
): NormalizedTooltipOptions {
  const placement = isPlacement(arg) ? arg : DEFAULT_OPTIONS.placement
  if (typeof value === 'string') {
    return { ...DEFAULT_OPTIONS, placement, content: value }
  }
  return {
    ...DEFAULT_OPTIONS,
    placement,
    ...value,
    content: value?.content ?? '',
  }
}

function isPlacement(value: unknown): value is TooltipPlacement {
  return (
    value === 'top' ||
    value === 'top-start' ||
    value === 'top-end' ||
    value === 'right' ||
    value === 'right-start' ||
    value === 'right-end' ||
    value === 'bottom' ||
    value === 'bottom-start' ||
    value === 'bottom-end' ||
    value === 'left' ||
    value === 'left-start' ||
    value === 'left-end'
  )
}

function createTooltip(): HTMLSpanElement {
  const tooltip = document.createElement('span')
  tooltip.className = 'app-tooltip'
  tooltip.setAttribute('role', 'tooltip')
  return tooltip
}

function addDescribedBy(el: HTMLElement, id: string): void {
  const current = el.getAttribute('aria-describedby')
  const ids = current?.split(/\s+/).filter(Boolean) ?? []
  if (ids.includes(id)) return
  ids.push(id)
  el.setAttribute('aria-describedby', ids.join(' '))
}

function removeDescribedBy(el: HTMLElement, id: string): void {
  const current = el.getAttribute('aria-describedby')
  if (current === null) return
  const ids = current.split(/\s+/).filter((item) => item !== id)
  if (ids.length === 0) {
    el.removeAttribute('aria-describedby')
    return
  }
  el.setAttribute('aria-describedby', ids.join(' '))
}

function splitPlacement(placement: TooltipPlacement): [TooltipBasePlacement, TooltipAlignment] {
  const [base, alignment] = placement.split('-') as [TooltipBasePlacement, TooltipAlignment]
  return [base, alignment]
}

function withBasePlacement(
  base: TooltipBasePlacement,
  preferred: TooltipPlacement,
): TooltipPlacement {
  const [, alignment] = splitPlacement(preferred)
  return (alignment === undefined ? base : `${base}-${alignment}`) as TooltipPlacement
}

function resolvePlacement(
  preferred: TooltipPlacement,
  triggerRect: DOMRect,
  tooltipRect: DOMRect,
): TooltipPlacement {
  const [base] = splitPlacement(preferred)
  const margin = 8
  const offset = 8

  if (
    base === 'top' &&
    triggerRect.top - tooltipRect.height - offset < margin &&
    triggerRect.bottom + tooltipRect.height + offset <= window.innerHeight - margin
  ) {
    return withBasePlacement('bottom', preferred)
  }

  if (
    base === 'bottom' &&
    triggerRect.bottom + tooltipRect.height + offset > window.innerHeight - margin &&
    triggerRect.top - tooltipRect.height - offset >= margin
  ) {
    return withBasePlacement('top', preferred)
  }

  if (
    base === 'left' &&
    triggerRect.left - tooltipRect.width - offset < margin &&
    triggerRect.right + tooltipRect.width + offset <= window.innerWidth - margin
  ) {
    return withBasePlacement('right', preferred)
  }

  if (
    base === 'right' &&
    triggerRect.right + tooltipRect.width + offset > window.innerWidth - margin &&
    triggerRect.left - tooltipRect.width - offset >= margin
  ) {
    return withBasePlacement('left', preferred)
  }

  return preferred
}

function clamp(value: number, min: number, max: number): number {
  if (max < min) return min
  return Math.min(Math.max(value, min), max)
}

function calculatePosition(
  triggerRect: DOMRect,
  tooltipRect: DOMRect,
  placement: TooltipPlacement,
): { top: number; left: number } {
  const [base, alignment] = splitPlacement(placement)
  const margin = 8
  const offset = 8
  let top = 0
  let left = 0

  if (base === 'top' || base === 'bottom') {
    top = base === 'top'
      ? triggerRect.top - tooltipRect.height - offset
      : triggerRect.bottom + offset

    if (alignment === 'start') {
      left = triggerRect.left
    } else if (alignment === 'end') {
      left = triggerRect.right - tooltipRect.width
    } else {
      left = triggerRect.left + (triggerRect.width - tooltipRect.width) / 2
    }
  } else {
    left = base === 'left'
      ? triggerRect.left - tooltipRect.width - offset
      : triggerRect.right + offset

    if (alignment === 'start') {
      top = triggerRect.top
    } else if (alignment === 'end') {
      top = triggerRect.bottom - tooltipRect.height
    } else {
      top = triggerRect.top + (triggerRect.height - tooltipRect.height) / 2
    }
  }

  return {
    top: clamp(top, margin, window.innerHeight - tooltipRect.height - margin),
    left: clamp(left, margin, window.innerWidth - tooltipRect.width - margin),
  }
}

function createState(el: HTMLElement, binding: DirectiveBinding<TooltipDirectiveValue>): TooltipState {
  const state: TooltipState = {
    id: `app-tooltip-${++tooltipIdSeed}`,
    options: normalizeOptions(binding.value, binding.arg),
    tooltip: null,
    show: () => showTooltip(el, state),
    hide: () => hideTooltip(el, state),
    updatePosition: () => updateTooltipPosition(el, state),
    cleanupElement: () => {},
    cleanupGlobal: null,
    rafId: null,
    hideTimer: null,
  }

  const onMouseEnter = state.show
  const onMouseLeave = () => scheduleTooltipHide(el, state)
  const onFocusIn = state.show
  const onFocusOut = state.hide

  el.addEventListener('mouseenter', onMouseEnter)
  el.addEventListener('mouseleave', onMouseLeave)
  el.addEventListener('focusin', onFocusIn)
  el.addEventListener('focusout', onFocusOut)

  state.cleanupElement = () => {
    el.removeEventListener('mouseenter', onMouseEnter)
    el.removeEventListener('mouseleave', onMouseLeave)
    el.removeEventListener('focusin', onFocusIn)
    el.removeEventListener('focusout', onFocusOut)
  }

  return state
}

function attachGlobalListeners(state: TooltipState): void {
  if (state.cleanupGlobal !== null) return

  const onUpdate = () => state.updatePosition()
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') state.hide()
  }

  window.addEventListener('resize', onUpdate)
  document.addEventListener('scroll', onUpdate, true)
  document.addEventListener('keydown', onKeyDown)

  state.cleanupGlobal = () => {
    window.removeEventListener('resize', onUpdate)
    document.removeEventListener('scroll', onUpdate, true)
    document.removeEventListener('keydown', onKeyDown)
    state.cleanupGlobal = null
  }
}

function showTooltip(el: HTMLElement, state: TooltipState): void {
  if (state.options.disabled || state.options.content.trim() === '') return
  cancelTooltipHide(state)

  if (state.tooltip === null) {
    state.tooltip = createTooltip()
    state.tooltip.id = state.id
    state.tooltip.addEventListener('mouseenter', () => cancelTooltipHide(state))
    state.tooltip.addEventListener('mouseleave', () => scheduleTooltipHide(el, state))
  }

  state.tooltip.textContent = state.options.content
  state.tooltip.dataset.placement = state.options.placement
  state.tooltip.classList.toggle('is-wrap', state.options.wrap)

  if (!document.body.contains(state.tooltip)) {
    document.body.appendChild(state.tooltip)
  }

  addDescribedBy(el, state.id)
  attachGlobalListeners(state)

  if (state.rafId !== null) {
    window.cancelAnimationFrame(state.rafId)
  }

  state.rafId = window.requestAnimationFrame(() => {
    state.rafId = null
    state.updatePosition()
    state.tooltip?.classList.add('is-visible')
  })
}

function hideTooltip(el: HTMLElement, state: TooltipState): void {
  cancelTooltipHide(state)
  if (state.rafId !== null) {
    window.cancelAnimationFrame(state.rafId)
    state.rafId = null
  }

  state.tooltip?.classList.remove('is-visible')
  state.tooltip?.remove()
  state.cleanupGlobal?.()
  removeDescribedBy(el, state.id)
}

function cancelTooltipHide(state: TooltipState): void {
  if (state.hideTimer === null) return
  window.clearTimeout(state.hideTimer)
  state.hideTimer = null
}

function scheduleTooltipHide(el: HTMLElement, state: TooltipState): void {
  cancelTooltipHide(state)
  // 让鼠标能够从触发元素移动至悬浮层，从而选择、复制其中的长文本。
  state.hideTimer = window.setTimeout(() => {
    state.hideTimer = null
    hideTooltip(el, state)
  }, 120)
}

function updateTooltipPosition(el: HTMLElement, state: TooltipState): void {
  if (state.tooltip === null || !document.body.contains(state.tooltip)) return

  const triggerRect = el.getBoundingClientRect()
  const tooltipRect = state.tooltip.getBoundingClientRect()
  const placement = resolvePlacement(state.options.placement, triggerRect, tooltipRect)
  const position = calculatePosition(triggerRect, tooltipRect, placement)

  state.tooltip.dataset.placement = placement
  state.tooltip.style.top = `${Math.round(position.top)}px`
  state.tooltip.style.left = `${Math.round(position.left)}px`
}

export const tooltipDirective: Directive<HTMLElement, TooltipDirectiveValue> = {
  mounted(el, binding) {
    tooltipStates.set(el, createState(el, binding))
  },
  updated(el, binding) {
    const state = tooltipStates.get(el)
    if (state === undefined) return

    state.options = normalizeOptions(binding.value, binding.arg)
    if (state.tooltip !== null) {
      state.tooltip.textContent = state.options.content
      state.tooltip.classList.toggle('is-wrap', state.options.wrap)
    }

    if (state.options.disabled || state.options.content.trim() === '') {
      state.hide()
      return
    }

    state.updatePosition()
  },
  beforeUnmount(el) {
    const state = tooltipStates.get(el)
    if (state === undefined) return

    state.hide()
    state.cleanupElement()
    tooltipStates.delete(el)
  },
}

export default tooltipDirective
