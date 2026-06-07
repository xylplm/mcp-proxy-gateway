/**
 * 复制到剪贴板的轻量 composable。
 *
 * 优先使用安全上下文下的 navigator.clipboard；在非 HTTPS / 不支持的环境降级到
 * 临时 textarea + execCommand('copy')，最大化兼容性（含局域网 http 访问场景）。
 */
import { ref } from 'vue'

export function useClipboard() {
  /** 最近一次复制是否成功（供 UI 做短暂"已复制"反馈）。 */
  const copied = ref(false)

  /**
   * 复制文本到剪贴板。
   * @param text 待复制文本
   * @returns 是否复制成功
   */
  async function copy(text: string): Promise<boolean> {
    const ok = await writeClipboard(text)
    copied.value = ok
    if (ok) {
      // 1.5s 后自动复位"已复制"反馈。
      window.setTimeout(() => {
        copied.value = false
      }, 1500)
    }
    return ok
  }

  return { copied, copy }
}

/** 实际写入剪贴板：优先 Clipboard API，失败时降级 execCommand。 */
async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 落到下方降级方案
  }
  return fallbackCopy(text)
}

/** 降级复制：临时 textarea + document.execCommand('copy')。 */
function fallbackCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}
