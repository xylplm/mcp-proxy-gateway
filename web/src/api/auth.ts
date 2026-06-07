/**
 * 鉴权相关 API 封装
 *
 * 设计要点（对应 design.md「鉴权中间件」与 Req 1.4 / 17.6）：
 * - 管理员登录：POST /api/auth/login（公开端点，无需 JWT），请求体 { username, password }；
 * - 登录成功后端返回管理员 JWT，后续请求经 Axios 请求拦截器注入 Authorization: Bearer。
 *
 * 请求/响应契约：
 * - 请求：POST /api/auth/login，body: { username: string, password: string }
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
  /** 管理员密码（长度 6-128，校验由后端负责，Req 1.2/1.9） */
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
  // 公开端点 /api/auth/login 与 axios baseURL `/api/admin` 不同前缀，需显式覆盖。
  // 响应拦截器已解包统一信封，response.data 即为内层数据 { token, expiresAt }。
  const response = await request.post<LoginResponseBody>('/auth/login', payload, {
    baseURL: '/api',
  })
  const token = response.data?.token
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

/** 认证状态响应：是否已完成首次初始化 + 离线密码重置标记文件名（Req 1.1） */
export interface AuthStatus {
  /** 是否已完成管理员首次初始化；为 false 时前端应展示注册入口。 */
  initialized: boolean
  /** 离线密码重置标记文件名，前端「忘记密码」弹窗据此提示用户。 */
  resetMarkerFile: string
}

/**
 * 获取认证初始化状态（公开端点，无需鉴权）。
 *
 * 失败时统一返回安全默认值（按已初始化处理）以避免在网络异常时无意暴露注册入口。
 */
export async function getAuthStatus(): Promise<AuthStatus> {
  // 公开端点 /api/auth/status 与 baseURL `/api/admin` 不同前缀，使用 baseURL 反推。
  const response = await request.get<AuthStatus>('/auth/status', { baseURL: '/api' })
  return {
    initialized: response.data?.initialized ?? true,
    resetMarkerFile: response.data?.resetMarkerFile ?? '.reset-admin',
  }
}

/** 注册请求体（首次初始化），与登录复用相同的字段定义。 */
export type RegisterRequest = LoginRequest

/**
 * 首次初始化：注册唯一管理员账号（Req 1.2、1.3）。
 *
 * 公开端点 POST /api/auth/register，已初始化时后端返回 CONFLICT。
 */
export async function register(payload: RegisterRequest): Promise<void> {
  await request.post('/auth/register', payload, { baseURL: '/api' })
}
