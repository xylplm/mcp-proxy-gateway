/**
 * Axios 全局请求实例
 *
 * 设计要点（对应 design.md「请求层」「鉴权中间件」「错误模型」与 Req 17.4 / 17.6）：
 * - baseURL 统一指向管理 REST API 前缀 `/api/admin`，与后端路由约定一致；
 * - 请求拦截器：从会话 store 读取管理员 JWT 并注入 `Authorization: Bearer <token>`（Req 17.6）；
 * - 响应拦截器：当后端返回鉴权失败/令牌失效（HTTP 401 或统一错误模型 code=UNAUTHORIZED）时，
 *   清除本地会话并重定向到登录页（Req 17.6、design.md 错误模型「令牌缺失/无效/过期」）。
 *
 * 注意：本文件会引用路由单例与会话 store。
 * - 会话 store（Pinia）在拦截器回调内按需获取，确保 Pinia 已完成初始化；
 * - 登录路由在 router 中以懒加载方式注册，避免 request → router → LoginView → auth → request 的模块循环依赖。
 */
import axios, { type AxiosInstance, type InternalAxiosRequestConfig } from 'axios'
import router from '@/router'
import { useSessionStore } from '@/stores/session'

/** 管理后台 API 基础路径前缀 */
export const ADMIN_API_BASE_URL = '/api/admin'

/**
 * 后端统一错误响应体（与 design.md「错误模型」对齐）。
 * 仅声明前端关心的字段，便于在响应拦截器中识别鉴权失败类别。
 */
interface ApiErrorBody {
  /** 错误类别，如 UNAUTHORIZED / VALIDATION / NOT_FOUND 等 */
  code?: string
  /** 人类可读的错误描述 */
  message?: string
}

/** 全局共享的 Axios 实例 */
const request: AxiosInstance = axios.create({
  baseURL: ADMIN_API_BASE_URL,
  // 默认超时时间（毫秒），可按需调整
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json',
  },
})

// 请求拦截器：注入管理员 JWT（Req 17.6）
request.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const session = useSessionStore()
    // 已登录时携带 Bearer 令牌；未登录则不注入，由后端拒绝并触发 401 处理
    if (session.token !== null && session.token !== '') {
      config.headers.Authorization = `Bearer ${session.token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  },
)

// 响应拦截器：统一处理 401/令牌失效（清会话 + 重定向登录，Req 17.6）
request.interceptors.response.use(
  (response) => {
    // 正常响应直接透传，由上层 API 封装按契约解包
    return response
  },
  (error: unknown) => {
    if (isUnauthorizedError(error)) {
      // 鉴权失败/令牌失效：清除本地会话并重定向到登录页
      redirectToLogin()
    }
    return Promise.reject(error)
  },
)

/**
 * 判断错误是否为鉴权失败/令牌失效。
 * 兼容两类判定信号：
 * 1) HTTP 状态码 401（design.md 鉴权中间件失败返回 401）；
 * 2) 统一错误模型 body.code === 'UNAUTHORIZED'（design.md 错误模型）。
 */
function isUnauthorizedError(error: unknown): boolean {
  if (!axios.isAxiosError(error)) {
    return false
  }
  if (error.response?.status === 401) {
    return true
  }
  const body = error.response?.data as ApiErrorBody | undefined
  return body?.code === 'UNAUTHORIZED'
}

/**
 * 清除本地会话并重定向到登录页。
 * 通过 query.redirect 记录被拦截的目标路径，便于登录成功后回跳；
 * 已位于登录页时不重复跳转，避免出现导航循环。
 */
function redirectToLogin() {
  const session = useSessionStore()
  session.clearSession()

  const currentRoute = router.currentRoute.value
  if (currentRoute.name !== 'login') {
    void router.push({
      name: 'login',
      query: { redirect: currentRoute.fullPath },
    })
  }
}

export default request
