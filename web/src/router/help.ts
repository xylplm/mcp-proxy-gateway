/**
 * 帮助中心路由与目录清单。
 *
 * 单一数据源：helpTopicGroups 同时驱动路由注册与左侧目录。新增一篇文档只需在此
 * 追加一条并新建对应 .vue，不必改动布局或路由装配代码。
 *
 * 性能：/help 布局与每一篇文档都是独立 import()，与控制台主包完全分离。
 * 打开帮助中心不会拉取管理台代码，切换文档只按需加载当前那一篇。
 *
 * 鉴权：帮助文档为公开内容，不设 requiresAuth。否则会话过期时新窗口会被重定向到
 * 登录页，用户恰好在最需要文档的时候读不到文档。
 */
import type { Component } from 'vue'
import type { RouteRecordRaw } from 'vue-router'

/** 帮助文档懒加载器，形如 () => import('@/views/help/topics/X.vue')。 */
type HelpTopicLoader = () => Promise<{ default: Component }>

export interface HelpTopic {
  /** URL 片段，同时用于生成路由 name */
  id: string
  /** 目录与页头标题 */
  title: string
  /** 目录中的一句话说明 */
  summary: string
  /** 搜索关键词：含英文别名与控制台页面名，便于用户用任意叫法命中 */
  keywords: string[]
  component: HelpTopicLoader
}

export interface HelpTopicGroup {
  title: string
  topics: HelpTopic[]
}

/** 帮助中心根路径，供入口链接与布局共用。 */
export const HELP_BASE_PATH = '/help'

/** 首页路由名，供入口链接解析 href。 */
export const HELP_HOME_ROUTE = 'Help'

/** 按控制台功能模块组织的文档目录。 */
export const helpTopicGroups: HelpTopicGroup[] = [
  {
    title: 'MCP 管理',
    topics: [
      {
        id: 'upstreams',
        title: '接入上游 MCP',
        summary: '四种传输类型怎么选，模板市场一键接入，连接失败如何排查。',
        keywords: ['上游', 'upstream', 'stdio', 'sse', 'http', 'websocket', '模板市场', '连接'],
        component: () => import('@/views/help/topics/HelpUpstreams.vue'),
      },
      {
        id: 'tools',
        title: '工具目录与可见性',
        summary: '聚合后的工具从哪来，如何改名、隐藏、控制哪些客户端能看到。',
        keywords: ['工具', 'tools', '可见性', '别名', '重命名', '冲突', '缓存'],
        component: () => import('@/views/help/topics/HelpTools.vue'),
      },
      {
        id: 'rules',
        title: '规则引擎',
        summary: '用规则批量改写工具名称、描述与可见性，先预览再生效。',
        keywords: ['规则', 'rules', '改写', '优先级', '预览', '正则'],
        component: () => import('@/views/help/topics/HelpRules.vue'),
      },
    ],
  },
  {
    title: 'API 与接入',
    topics: [
      {
        id: 'api-service',
        title: '客户端接入网关',
        summary: '把网关配到 Claude、Cursor 等客户端，以及 OpenAPI 直接调用。',
        keywords: ['接入', '客户端', 'claude', 'cursor', 'openapi', '端点', 'endpoint', 'api 服务'],
        component: () => import('@/views/help/topics/HelpApiService.vue'),
      },
      {
        id: 'apikeys',
        title: 'API Key 与配额',
        summary: '创建密钥、限定可用工具、设置速率与配额，以及泄露后的处置。',
        keywords: ['apikey', 'api key', '密钥', '配额', '限流', '权限', 'acl'],
        component: () => import('@/views/help/topics/HelpApiKeys.vue'),
      },
      {
        id: 'observability',
        title: '统计、记录与日志',
        summary: '调用统计、调用记录、审计日志、系统日志各看什么，怎么定位问题。',
        keywords: ['统计', '记录', '日志', '审计', 'audit', 'log', '排查', '监控'],
        component: () => import('@/views/help/topics/HelpObservability.vue'),
      },
    ],
  },
  {
    title: '系统与运行',
    topics: [
      {
        id: 'runtime',
        title: '运行环境与依赖',
        summary: '镜像自带哪些运行时，npm / pip 共享依赖怎么装，缺工具如何补齐。',
        keywords: ['运行环境', 'runtime', 'node', 'python', 'uv', 'npm', 'pip', '依赖', '镜像'],
        component: () => import('@/views/help/topics/HelpRuntime.vue'),
      },
      {
        id: 'scripts',
        title: '脚本中心',
        summary: '把一段 Python / JavaScript 变成可调用的 MCP 上游，含版本与回滚。',
        keywords: ['脚本', 'script', 'python', 'javascript', '版本', '回滚', '语法检测'],
        component: () => import('@/views/help/topics/HelpScripts.vue'),
      },
      {
        id: 'security',
        title: '安全档位与安全中心',
        summary: '三档安全模式怎么选，文件与网络如何收敛，风险项在哪看。',
        keywords: ['安全', 'security', '严格', 'strict', '沙箱', 'bubblewrap', '白名单', '风险'],
        component: () => import('@/views/help/topics/HelpSecurity.vue'),
      },
      {
        id: 'settings',
        title: '系统设置',
        summary: '连接超时、重试退避、包仓库镜像、备份恢复等全局开关。',
        keywords: ['设置', 'settings', '超时', '重试', '镜像源', 'registry', '备份', '恢复'],
        component: () => import('@/views/help/topics/HelpSettings.vue'),
      },
    ],
  },
  {
    title: '疑难解答',
    topics: [
      {
        id: 'faq',
        title: '常见问题',
        summary: '连接不上、工具不显示、调用报错等高频问题的定位顺序。',
        keywords: ['faq', '常见问题', '报错', '失败', '排查', '故障'],
        component: () => import('@/views/help/topics/HelpFaq.vue'),
      },
    ],
  },
]

/** 扁平化后的全部文档，供搜索与上一篇/下一篇导航使用。 */
export const helpTopics: HelpTopic[] = helpTopicGroups.flatMap((group) => group.topics)

/** 由 id 生成路由名，入口链接与目录高亮共用同一规则。 */
export function helpTopicRouteName(id: string): string {
  return `Help:${id}`
}

/**
 * 帮助中心路由：/help 为壳与总览，/help/:id 为各模块文档。
 * 每个 component 都是独立 import()，因此构建产物按模块自然分包。
 */
export const helpRoutes: RouteRecordRaw[] = [
  {
    path: HELP_BASE_PATH,
    component: () => import('@/views/help/HelpLayout.vue'),
    meta: { requiresAuth: false },
    children: [
      {
        path: '',
        name: HELP_HOME_ROUTE,
        component: () => import('@/views/help/HelpHome.vue'),
        meta: { title: '帮助中心', requiresAuth: false },
      },
      ...helpTopics.map<RouteRecordRaw>((topic) => ({
        path: topic.id,
        name: helpTopicRouteName(topic.id),
        component: topic.component,
        meta: { title: `帮助 · ${topic.title}`, requiresAuth: false },
      })),
      // 失效的文档链接回到帮助首页，而不是掉进全局 404 页 —— 后者会整页替换掉
      // 帮助中心，用户还得重新找入口。
      { path: ':pathMatch(.*)*', redirect: { name: HELP_HOME_ROUTE } },
    ],
  },
]
