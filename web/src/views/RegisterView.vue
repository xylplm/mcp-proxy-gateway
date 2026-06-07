<script setup lang="ts">
/**
 * 首次初始化（注册）页（对应 Req 1.1、1.2、1.3 与 design.md「鉴权中间件」）。
 *
 * 单一职责：仅在系统未初始化时引导创建唯一管理员账号。
 * - 进入时调用公开端点 GET /api/auth/status：
 *   · initialized=true  → 切换到「已初始化」提示态，给出跳转登录的引导按钮（也可由路由守卫自动跳转）；
 *   · initialized=false → 渲染初始化表单（用户名 + 密码 + 二次确认密码）。
 * - 提交时调用 POST /api/auth/register，成功后立即用同凭证登录并按 query.redirect 回跳。
 */
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthShell from '@/components/layout/AuthShell.vue'
import { getAuthStatus, login, register, type RegisterRequest } from '@/api/auth'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const route = useRoute()
const session = useSessionStore()

/** 状态加载中（在拿到 /auth/status 之前不渲染表单，避免文案闪烁）。 */
const statusLoading = ref(true)

/** 是否已初始化（true 时进入「引导跳转登录」提示态，避免误注册）。 */
const alreadyInitialized = ref(false)

/** 提交进行中状态（按钮 loading + 防重复提交）。 */
const submitting = ref(false)

/** 提交失败时展示的错误提示文本，空串表示无错误。 */
const errorMessage = ref('')

/** 注册表单数据模型。 */
const formState = reactive<RegisterRequest>({
  username: '',
  password: '',
})

/** 二次确认密码（仅注册场景使用）。 */
const confirmPassword = ref('')

/**
 * 进入页面：拉取认证状态以决定渲染表单还是「已初始化」引导态。
 *
 * 拉取失败时按「未初始化」处理，让用户至少能尝试注册；后端会做权威校验，
 * 已初始化场景会返回 CONFLICT 由 catch 分支提示。
 */
onMounted(async () => {
  try {
    const status = await getAuthStatus()
    alreadyInitialized.value = status.initialized
  } catch {
    alreadyInitialized.value = false
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
  if (formState.password !== confirmPassword.value) {
    return '两次输入的密码不一致'
  }
  return null
}

/** 表单提交：注册成功后立即用同凭证登录，避免还要再点一次登录。 */
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
    await register(credentials)
    const token = await login(credentials)
    session.setToken(token)
    redirectAfterRegister()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '初始化失败，请稍后再试'
  } finally {
    submitting.value = false
  }
}

/** 注册后跳转：优先回跳 query.redirect，否则跳 Dashboard。 */
function redirectAfterRegister() {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect !== '') {
    void router.replace(redirect)
  } else {
    void router.replace({ name: 'Dashboard' })
  }
}

/** 引导跳转登录页：保留 query.redirect 以便登录后回跳原目标。 */
function goLogin() {
  const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
  void router.replace({ name: 'login', query: redirect ? { redirect } : {} })
}
</script>

<template>
  <AuthShell>
    <!-- 状态加载占位：高度与表单接近，避免布局抖动 -->
    <div v-if="statusLoading" class="flex h-64 items-center justify-center">
      <span class="text-sm text-gray-400">正在加载…</span>
    </div>

    <!-- 已初始化：引导用户去登录页 -->
    <template v-else-if="alreadyInitialized">
      <div class="flex flex-col items-center text-center">
        <p class="mb-4 rounded-lg bg-brand-50 px-4 py-2.5 text-sm text-brand-700 dark:bg-brand-500/10 dark:text-brand-300">
          系统已完成初始化
        </p>
        <p class="mb-6 text-sm text-gray-600 dark:text-gray-300">
          管理员账号已存在，请前往登录页登录。
        </p>
        <button
          type="button"
          class="flex h-11 w-full items-center justify-center rounded-lg bg-brand-500 text-sm font-medium text-white shadow-sm shadow-brand-500/30 transition hover:bg-brand-600"
          @click="goLogin"
        >
          前往登录
        </button>
      </div>
    </template>

    <!-- 未初始化：展示注册表单 -->
    <template v-else>
      <p class="mb-5 rounded-lg bg-brand-50 px-4 py-2.5 text-center text-sm text-brand-700 dark:bg-brand-500/10 dark:text-brand-300">
        首次使用，请创建管理员账号
      </p>

      <form novalidate @submit.prevent="handleSubmit">
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
              placeholder="为管理员账号设置用户名"
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
              autocomplete="new-password"
              placeholder="设置密码（6 至 128 位）"
              class="h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30"
            />
          </div>

          <div>
            <label
              for="confirm-password"
              class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400"
            >
              确认密码
            </label>
            <input
              id="confirm-password"
              v-model="confirmPassword"
              type="password"
              autocomplete="new-password"
              placeholder="请再次输入密码"
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
            {{ submitting ? '初始化中…' : '创建账号并登录' }}
          </button>

          <p class="text-center text-xs text-gray-500 dark:text-gray-400">
            已有管理员账号？
            <button
              type="button"
              class="text-brand-600 hover:underline dark:text-brand-400"
              @click="goLogin"
            >
              前往登录
            </button>
          </p>
        </div>
      </form>
    </template>
  </AuthShell>
</template>
