/**
 * 鉴权相关 API 封装
 *
 * 设计要点（对应 design.md「鉴权中间件」与 Req 1.4 / 17.6）：
 * - 管理员登录：POST /api/admin/auth/login，请求体 { username, password }；
 * - 登录成功后端返回管理员 JWT，后续请求经 Axios 请求拦截器注入 Authorization: Bearer。
 *
 * 请求/响应契约（与后端任务 19.2 约定，后端尚未实现时前端按此契约编写）：
 * - 请求：POST `${ADMIN_API_BASE_URL}/auth/login`，body: { username: string, password: string }
 * - 响应：返回 JWT，兼容两种包装形式：
 *     1) 直接返回 `{ token: string }`
 *     2) 统一信封返回 `{ data: { token: string } }`
 *   本封装统一解包为字符串 token，便于上层无感知地适配后端最终形态。
 */
import request from '@/api/request'

/** 登录请求体 */
export interface LoginRequest {
  /** 管理员用户名（长度 3-32，校验由后端负责，Req 1.2/1.9） */
  username: string
  /** 管理员密码（长度 8-128，校验由后端负责，Req 1.2/1.9） */
  password: string
}

/** 登录响应可能的两种包装形态 */
interface LoginResponseBody {
  /** 直接返回令牌 */
  token?: string
  /** 统一信封形式返回 */
  data?: {
    token?: string
  }
}

/**
 * 管理员登录。
 *
 * @param payload 用户名与密码
 * @returns 管理员访问令牌（JWT）字符串
 * @throws 当后端拒绝登录（如 401 鉴权失败，Req 1.5）或响应不含令牌时抛出错误
 */
export async function login(payload: LoginRequest): Promise<string> {
  const response = await request.post<LoginResponseBody>('/auth/login', payload)
  const body = response.data
  // 兼容 { token } 与 { data: { token } } 两种契约形态
  const token = body?.token ?? body?.data?.token
  if (token === undefined || token === '') {
    throw new Error('登录响应中未包含访问令牌')
  }
  return token
}

/** 当前管理员信息，对应后端 GET /api/admin/auth/me 响应。 */
export interface CurrentAdmin {
  /** 当前登录管理员用户名。 */
  username: string
}

/**
 * 获取当前登录管理员信息（Req 1.x / 17.6）。
 *
 * 调用受 JWT 保护的 GET `${ADMIN_API_BASE_URL}/auth/me`，经全局请求拦截器注入令牌；
 * 令牌缺失/失效时由响应拦截器统一处理（清会话并重定向登录）。
 *
 * @returns 当前管理员信息（含用户名）
 */
export async function getCurrentAdmin(): Promise<CurrentAdmin> {
  const response = await request.get<CurrentAdmin>('/auth/me')
  return { username: response.data?.username ?? '' }
}
