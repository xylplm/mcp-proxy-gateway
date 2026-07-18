import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '@/stores/session'

/**
 * 路由元信息扩展声明。
 * - requiresAuth：标记该路由为「受保护页面」，未认证访问时由全局守卫重定向到登录页（Req 17.4）。
 * - title：页面标题，用于设置 document.title。
 */
declare module 'vue-router' {
  interface RouteMeta {
    /** 是否需要登录鉴权后才能访问 */
    requiresAuth?: boolean
    /** 页面标题 */
    title?: string
  }
}

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  scrollBehavior(to, from, savedPosition) {
    return savedPosition || { left: 0, top: 0 }
  },
  routes: [
    {
      // 登录页：公开访问，不需要鉴权
      path: '/login',
      name: 'login',
      component: () => import('../views/LoginView.vue'),
      meta: {
        title: '登录',
        requiresAuth: false,
      },
    },
    {
      // 首次初始化（注册）页：公开访问；未初始化时由登录页自动跳来，此处独立路由便于直接访问。
      path: '/register',
      name: 'register',
      component: () => import('../views/RegisterView.vue'),
      meta: {
        title: '初始化',
        requiresAuth: false,
      },
    },
    {
      path: '/',
      name: 'Dashboard',
      component: () => import('../views/DashboardView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '仪表盘',
        requiresAuth: true,
      },
    },
    {
      path: '/upstreams',
      name: 'Upstreams',
      component: () => import('../views/UpstreamsView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '上游 MCP 管理',
        requiresAuth: true,
      },
    },
    {
      path: '/apikeys',
      name: 'APIKeys',
      component: () => import('../views/APIKeysView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: 'API Key 管理',
        requiresAuth: true,
      },
    },
    {
      path: '/rules',
      name: 'Rules',
      component: () => import('../views/RulesView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '规则管理',
        requiresAuth: true,
      },
    },
    {
      path: '/tools',
      name: 'Tools',
      component: () => import('../views/ToolsView.vue'),
      meta: {
        title: '工具目录',
        requiresAuth: true,
      },
    },
    {
      path: '/statistics',
      name: 'Statistics',
      component: () => import('../views/StatisticsView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '调用统计',
        requiresAuth: true,
      },
    },
    {
      path: '/audit',
      name: 'Audit',
      component: () => import('../views/AuditView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '审计日志',
        requiresAuth: true,
      },
    },
    {
      path: '/system-logs',
      name: 'SystemLogs',
      component: () => import('../views/SystemLogsView.vue'),
      meta: {
        title: '系统日志',
        requiresAuth: true,
      },
    },
    {
      path: '/api-service',
      name: 'APIService',
      component: () => import('../views/APIServiceView.vue'),
      meta: {
        title: 'API 服务',
        requiresAuth: true,
      },
    },
    {
      path: '/call-records',
      name: 'CallRecords',
      component: () => import('../views/CallRecordsView.vue'),
      meta: {
        title: '调用记录',
        requiresAuth: true,
      },
    },
    {
      path: '/settings',
      name: 'Settings',
      component: () => import('../views/SettingsView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '系统设置',
        requiresAuth: true,
      },
    },
    {
      path: '/runtime',
      name: 'RuntimeEnvironment',
      component: () => import('../views/RuntimeEnvironmentView.vue'),
      meta: {
        title: '运行环境',
        requiresAuth: true,
      },
    },
    {
      path: '/security',
      name: 'SecurityCenter',
      component: () => import('../views/SecurityCenterView.vue'),
      meta: {
        title: '安全中心',
        requiresAuth: true,
      },
    },
    {
      path: '/about',
      name: 'About',
      component: () => import('../views/AboutView.vue'),
      meta: {
        title: '关于',
        requiresAuth: true,
      },
    },
    {
      path: '/profile',
      name: 'Profile',
      component: () => import('../views/ProfileView.vue'),
      // 受保护页面：需登录后访问（Req 17.4）
      meta: {
        title: '个人中心',
        requiresAuth: true,
      },
    },
    {
      path: '/:pathMatch(.*)*',
      name: '404 Error',
      component: () => import('../views/Errors/FourZeroFour.vue'),
      meta: {
        title: '页面不存在',
      },
    },
  ],
})

export default router

/**
 * 全局前置守卫：
 * - 设置 document.title（保留原模板行为）；
 * - 未认证用户访问受保护页面时重定向到登录页（Req 17.4），通过 query.redirect 记录原始目标路径；
 * - 已认证用户访问登录页时直接跳转到 Dashboard，避免重复登录。
 */
router.beforeEach((to) => {
  document.title = `${to.meta.title ?? 'MCP Proxy Gateway'} | MCP Proxy Gateway`

  const session = useSessionStore()

  // 受保护页面且未认证 → 重定向到登录页，并携带回跳地址
  if (to.meta.requiresAuth === true && !session.isAuthenticated) {
    return {
      name: 'login',
      query: { redirect: to.fullPath },
    }
  }

  // 已认证用户访问登录/注册页 → 重定向到 Dashboard，避免重复操作
  if ((to.name === 'login' || to.name === 'register') && session.isAuthenticated) {
    return { name: 'Dashboard' }
  }

  // 其余情况放行
  return true
})
