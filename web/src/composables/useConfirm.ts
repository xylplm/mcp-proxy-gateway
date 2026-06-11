import { computed, ref } from 'vue'

export type ConfirmTone = 'danger' | 'warning' | 'info'

export interface ConfirmOptions {
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  tone?: ConfirmTone
}

interface ConfirmState extends Required<ConfirmOptions> {
  open: boolean
}

const defaultState: ConfirmState = {
  open: false,
  title: '',
  message: '',
  confirmText: '确认',
  cancelText: '取消',
  tone: 'danger',
}

const state = ref<ConfirmState>({ ...defaultState })
let resolver: ((value: boolean) => void) | null = null

function confirm(options: ConfirmOptions): Promise<boolean> {
  if (resolver !== null) {
    resolver(false)
  }
  state.value = {
    open: true,
    title: options.title,
    message: options.message,
    confirmText: options.confirmText ?? '确认',
    cancelText: options.cancelText ?? '取消',
    tone: options.tone ?? 'danger',
  }
  return new Promise((resolve) => {
    resolver = resolve
  })
}

function resolveConfirm(value: boolean): void {
  state.value = { ...defaultState }
  resolver?.(value)
  resolver = null
}

export function useConfirm() {
  return {
    confirm,
    confirmState: computed(() => state.value),
    resolveConfirm,
  }
}
