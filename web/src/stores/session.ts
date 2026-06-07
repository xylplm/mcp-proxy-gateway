/**
 * 会话状态 Store
 *
 * 设计要点（对应 design.md「状态管理 Pinia」与 Req 17.4 / 17.6）：
 * - 管理管理员登录态与令牌，作为路由守卫与请求拦截器的会话来源；
 * - 令牌持久化到 localStorage，store 初始化时自动恢复，刷新页面后保持登录态；
 * - 登录成功调用 setToken 写入并持久化；登出或令牌失效（401）调用 clearSession 清除。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

/** localStorage 中持久化管理员令牌的键名 */
export const TOKEN_STORAGE_KEY = 'mcp-gateway-admin-token'

export const useSessionStore = defineStore('session', () => {
  /**
   * 管理员访问令牌（JWT），未登录时为 null。
   * 初始化时从 localStorage 恢复，实现刷新后保持登录态（Req 17.6）。
   */
  const token = ref<string | null>(readPersistedToken())

  /**
   * 当前登录管理员用户名，未知时为 null。
   * 由用户菜单等组件调用 GET /auth/me 后通过 setUsername 写入；
   * 仅保存在内存中，刷新后按需重新拉取（保持简单，不持久化）。
   */
  const username = ref<string | null>(null)

  /** 是否已认证（存在有效令牌） */
  const isAuthenticated = computed(() => token.value !== null && token.value !== '')

  /** 设置令牌（登录成功后调用），同时持久化到 localStorage */
  function setToken(newToken: string) {
    token.value = newToken
    persistToken(newToken)
  }

  /** 记录当前管理员用户名（拉取 /auth/me 成功后调用）。 */
  function setUsername(name: string) {
    username.value = name
  }

  /** 清除会话（登出或令牌失效时调用），同时清除 localStorage 中的令牌 */
  function clearSession() {
    token.value = null
    username.value = null
    persistToken(null)
  }

  return { token, username, isAuthenticated, setToken, setUsername, clearSession }
})

/** 从 localStorage 读取已持久化的令牌；读取失败或为空时返回 null */
function readPersistedToken(): string | null {
  try {
    const stored = localStorage.getItem(TOKEN_STORAGE_KEY)
    return stored !== null && stored !== '' ? stored : null
  } catch {
    // 某些隐私模式/受限环境下访问 localStorage 会抛错，降级为纯内存态
    return null
  }
}

/** 将令牌写入或从 localStorage 移除；传入 null/空串表示移除 */
function persistToken(value: string | null) {
  try {
    if (value === null || value === '') {
      localStorage.removeItem(TOKEN_STORAGE_KEY)
    } else {
      localStorage.setItem(TOKEN_STORAGE_KEY, value)
    }
  } catch {
    // 忽略持久化失败，保证不影响主流程
  }
}
