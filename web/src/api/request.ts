/**
 * Axios 全局请求实例与统一响应处理
 *
 * 设计要点（对应 design.md「请求层」「鉴权中间件」「错误模型」与 Req 17.4 / 17.6）：
 * - baseURL 统一指向管理 REST API 前缀 `/api/admin`，与后端路由约定一致；
 * - 请求拦截器：从会话 store 读取管理员 JWT 并注入 `Authorization: Bearer <token>`（Req 17.6）；
 * - 响应拦截器（统一信封）：后端管理端点统一返回信封 `{ code, message, data }`：
 *     · 成功（code === 20000）：把响应体替换为内层 `data`，于是上层 `request.get<T>` 的
 *       `res.data` 直接是业务数据 T，无需各处再 `.data.data`；
 *     · 业务失败（HTTP 2xx 但 code !== 20000，理论上少见）：构造带 code/message/fields 的
 *       ApiError 并 reject；
 *     · 传输/系统失败（HTTP 4xx/5xx）：从信封提取 message/code/fields 构造 ApiError 并 reject；
 *       HTTP 401 额外清会话并重定向登录（Req 17.6）。
 *
 * 默认提示：业务侧 catch 到的统一是 ApiError，其 message 即后端中文提示，可直接展示；
 * 同时保留 code 与 fields，便于业务按需做字段级处理或精细分支。
 *
 * 注意：本文件会引用路由单例与会话 store。
 * - 会话 store（Pinia）在拦截器回调内按需获取，确保 Pinia 已完成初始化；
 * - 登录路由在 router 中以懒加载方式注册，避免模块循环依赖。
 */
import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'
import router from '@/router'
import { useSessionStore } from '@/stores/session'

/** 管理后台 API 基础路径前缀 */
export const ADMIN_API_BASE_URL = '/api/admin'

/** 成功响应的统一业务码，与后端 httpapi 的 codeSuccess 对齐。 */
export const CODE_SUCCESS = 20000

/**
 * 后端统一响应信封。
 *
 * - code：数字业务状态码（成功 20000，失败见后端 codeToBusinessCode）；
 * - message：人类可读提示，可直接用于默认错误展示；
 * - data：业务数据载荷；失败时为 null 或 { fields } 字段级明细。
 */
export interface Envelope<T = unknown> {
  code: number
  message: string
  data: T
}

/**
 * 统一业务错误。
 *
 * 由响应拦截器在请求失败（HTTP 错误或业务码非成功）时抛出，业务侧 `catch (e)` 后
 * 可直接读取 message 展示，或据 code/fields 做精细处理。
 */
export class ApiError extends Error {
  /** 数字业务状态码。 */
  readonly code: number
  /** HTTP 状态码（若有响应）。 */
  readonly httpStatus?: number
  /** 字段级校验明细（来自信封 data.fields），无则为空对象。 */
  readonly fields: Record<string, string>

  constructor(
    message: string,
    code: number,
    httpStatus?: number,
    fields?: Record<string, string>,
  ) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.httpStatus = httpStatus
    this.fields = fields ?? {}
  }
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
  (error) => Promise.reject(error),
)

// 响应拦截器：成功解包信封 data；失败统一构造 ApiError 并处理 401（Req 17.6）
request.interceptors.response.use(
  (response: AxiosResponse) => {
    const body = response.data as Envelope | undefined
    // 兼容非信封响应（如个别非管理端点）：无 code 字段时原样透传。
    if (body == null || typeof body.code !== 'number') {
      return response
    }
    if (body.code === CODE_SUCCESS) {
      // 解包：把响应体替换为内层 data，上层 res.data 即业务数据。
      response.data = body.data
      return response
    }
    // HTTP 2xx 但业务码非成功：按业务错误抛出。
    return Promise.reject(
      new ApiError(body.message || '请求失败', body.code, response.status, extractFields(body.data)),
    )
  },
  (error: unknown) => {
    if (axios.isAxiosError(error)) {
      const status = error.response?.status
      const body = error.response?.data as Envelope | undefined

      if (status === 401) {
        // 鉴权失败/令牌失效：清除本地会话并重定向到登录页
        redirectToLogin()
      }

      const message =
        body?.message || error.message || '网络异常，请稍后重试'
      const code = typeof body?.code === 'number' ? body.code : (status ?? 0)
      return Promise.reject(new ApiError(message, code, status, extractFields(body?.data)))
    }
    return Promise.reject(error)
  },
)

/**
 * 泛型请求：直接返回业务数据 T（已由响应拦截器解包信封）。
 *
 * 用法：`const list = await requestData<Upstream[]>({ url: '/upstreams' })`。
 * 失败时抛出 ApiError（含 message/code/fields），由调用方按需 catch。
 */
export async function requestData<T>(config: AxiosRequestConfig): Promise<T> {
  const res = await request.request<T>(config)
  return res.data as T
}

/** 从信封 data 中提取字段级校验明细（data.fields），无则返回空对象。 */
function extractFields(data: unknown): Record<string, string> {
  const fields = (data as { fields?: Record<string, string> } | null)?.fields
  return fields ?? {}
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
