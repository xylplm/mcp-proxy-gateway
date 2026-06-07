<script setup lang="ts">
/**
 * 登录页（对应 Req 1.1、1.4、17.4、17.6 与 design.md「鉴权中间件」）。
 *
 * 单一职责：只负责管理员登录。
 * - 进入时调用公开端点 GET /api/auth/status：
 *   · initialized=false → 自动跳转到 /register，由首次初始化页接管；
 *   · initialized=true  → 渲染登录表单。
 * - 提交时调用 POST /api/admin/auth/login，成功后保存 token 并按 query.redirect 回跳。
 * - 提供「忘记密码」入口，弹窗说明离线密码重置流程（在 data 目录创建标记文件并重启）。
 */
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthShell from '@/components/layout/AuthShell.vue'
import { getAuthStatus, login, type LoginRequest } from '@/api/auth'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const route = useRoute()
const session = useSessionStore()

/** 状态加载中（在拿到 /auth/status 之前不渲染表单，避免文案闪烁）。 */
const statusLoading = ref(true)

/** 离线密码重置标记文件名（来自后端动态返回，避免前后端文案脱节）。 */
const resetMarkerFile = ref('.reset-admin')

/** 提交进行中状态（按钮 loading + 防重复提交）。 */
const submitting = ref(false)

/** 提交失败时展示的错误提示文本，空串表示无错误。 */
const errorMessage = ref('')

/** 登录表单数据模型。 */
const formState = reactive<LoginRequest>({
  username: '',
  password: '',
})

/** 「忘记密码」弹窗开关。 */
const forgotOpen = ref(false)

/**
 * 进入页面：拉取认证状态以决定是否需要跳转到首次初始化页。
 *
 * 拉取失败时按「已初始化」处理（更安全），用户仍可正常登录或点击「忘记密码」。
 */
onMounted(async () => {
  try {
    const status = await getAuthStatus()
    if (!status.initialized) {
      const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
      void router.replace({ name: 'register', query: redirect ? { redirect } : {} })
      return
    }
    resetMarkerFile.value = status.resetMarkerFile || '.reset-admin'
  } catch {
    // 静默回落到登录模式（更安全，避免在网络异常时无意暴露注册入口）
  } finally {
    statusLoading.value = false
  }
})

/**
 * 基础客户端校验（与后端 Req 1.2/1.9 一致），权威校验仍由后端负责。
 *
 * @returns 校验通过返回 null，否则返回错误提示文本
 */
function validateForm(): string | null {
  const username = formState.username.trim()
  if (username.length < 3 || username.length > 32) {
    return '用户名长度需为 3 至 32 个字符'
  }
  if (formState.password.length < 6 || formState.password.length > 128) {
    return '密码长度需为 6 至 128 个字符'
  }
  return null
}

/** 表单提交：登录成功后保存 token 并跳转。 */
async function handleSubmit() {
  if (submitting.value) {
    return
  }

  errorMessage.value = ''
  const validationError = validateForm()
  if (validationError !== null) {
    errorMessage.value = validationError
    return
  }

  submitting.value = true
  try {
    const credentials = {
      username: formState.username.trim(),
      password: formState.password,
    }
    const token = await login(credentials)
    session.setToken(token)
    redirectAfterLogin()
  } catch (error) {
    errorMessage.value =
      error instanceof Error ? error.message : '登录失败，请检查用户名或密码'
  } finally {
    submitting.value = false
  }
}

/** 登录成功后跳转：优先回跳被守卫拦截前的目标路径，否则 Dashboard。 */
function redirectAfterLogin() {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect !== '') {
    void router.replace(redirect)
  } else {
    void router.replace({ name: 'Dashboard' })
  }
}
</script>

<template>
  <AuthShell>
    <!-- 状态加载占位：高度与表单接近，避免布局抖动 -->
    <div v-if="statusLoading" class="flex h-64 items-center justify-center">
      <span class="text-sm text-gray-400">正在加载…</span>
    </div>

    <form v-else novalidate @submit.prevent="handleSubmit">
      <div class="space-y-5">
        <div>
          <label
            for="username"
            class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400"
          >
            用户名
          </label>
          <input
            id="username"
            v-model="formState.username"
            type="text"
            autocomplete="username"
            placeholder="请输入管理员用户名"
            class="h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30"
          />
        </div>

        <div>
          <label
            for="password"
            class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400"
          >
            密码
          </label>
          <input
            id="password"
            v-model="formState.password"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
            class="h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30"
          />
        </div>

        <p
          v-if="errorMessage !== ''"
          role="alert"
          class="rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ errorMessage }}
        </p>

        <button
          type="submit"
          :disabled="submitting"
          class="flex h-11 w-full items-center justify-center rounded-lg bg-brand-500 text-sm font-medium text-white shadow-sm shadow-brand-500/30 transition hover:bg-brand-600 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {{ submitting ? '登录中…' : '登录' }}
        </button>

        <p class="text-center text-xs text-gray-500 dark:text-gray-400">
          <button
            type="button"
            class="text-brand-600 hover:underline dark:text-brand-400"
            @click="forgotOpen = true"
          >
            忘记密码？
          </button>
        </p>
      </div>
    </form>

    <!-- 「忘记密码」弹窗：说明离线密码重置流程 -->
    <transition name="fade">
      <div
        v-if="forgotOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="forgotOpen = false"
      >
        <div
          class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <h3 class="mb-3 text-base font-semibold text-gray-800 dark:text-white/90">忘记密码？</h3>
          <p class="mb-3 text-sm text-gray-600 dark:text-gray-300">
            出于安全考虑，密码重置需要主机的本地文件系统访问权限：
          </p>
          <ol class="mb-4 list-decimal space-y-1.5 pl-5 text-sm text-gray-600 dark:text-gray-300">
            <li>
              在网关的
              <code class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-800 dark:bg-gray-800 dark:text-gray-200">data</code>
              目录下创建一个空文件
              <code class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-800 dark:bg-gray-800 dark:text-gray-200">{{ resetMarkerFile }}</code>
            </li>
            <li>重启网关进程</li>
            <li>启动日志中会打印一次性的新密码，请立即记录</li>
            <li>该标记文件在重置完成后会被自动删除</li>
          </ol>
          <p class="mb-5 rounded-lg bg-warning-50 px-3 py-2 text-xs text-warning-700 dark:bg-warning-500/10 dark:text-warning-300">
            提示：该机制仅依赖本地文件系统权限，不暴露任何远程接口；密码仅在控制台显示一次，请勿截图或泄露。
          </p>
          <div class="flex justify-end">
            <button
              type="button"
              class="rounded-lg bg-brand-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
              @click="forgotOpen = false"
            >
              我知道了
            </button>
          </div>
        </div>
      </div>
    </transition>
  </AuthShell>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
