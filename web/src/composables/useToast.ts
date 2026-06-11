import { computed, ref } from 'vue'

export type ToastType = 'success' | 'warning' | 'error' | 'info'

export interface ToastOptions {
  duration?: number
}

export interface ToastItem {
  id: number
  type: ToastType
  message: string
}

const toasts = ref<ToastItem[]>([])
let nextToastID = 1

function pushToast(type: ToastType, message: string, options: ToastOptions = {}): number {
  const id = nextToastID++
  toasts.value = [{ id, type, message }]

  const duration = options.duration ?? 2200
  if (duration > 0) {
    window.setTimeout(() => removeToast(id), duration)
  }
  return id
}

function removeToast(id: number): void {
  toasts.value = toasts.value.filter((item) => item.id !== id)
}

export function useToast() {
  return {
    toasts: computed(() => toasts.value),
    removeToast,
    success: (message: string, options?: ToastOptions) => pushToast('success', message, options),
    warning: (message: string, options?: ToastOptions) => pushToast('warning', message, options),
    error: (message: string, options?: ToastOptions) => pushToast('error', message, options),
    info: (message: string, options?: ToastOptions) => pushToast('info', message, options),
  }
}
