<script setup lang="ts">
/**
 * 管理员登录页（对应 Req 17.4 / 17.6 与 design.md「鉴权中间件」）。
 *
 * 流程：
 * 1. 用户名 + 密码表单，前端做基础非空/长度校验（最终校验由后端负责，Req 1.2/1.9）；
 * 2. 提交时调用登录 API（POST /api/admin/auth/login），成功后将返回的 JWT 存入会话 store；
 * 3. 登录成功后跳转：优先回跳到被守卫拦截前的目标路径（query.redirect），否则进入 Dashboard。
 *
 * 样式：采用 Tailwind CSS / TailAdmin 风格，不依赖任何第三方组件库。
 */
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import FullScreenLayout from '@/components/layout/FullScreenLayout.vue'
import { login, type LoginRequest } from '@/api/auth'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const route = useRoute()
const session = useSessionStore()

/** 登录请求中状态，用于按钮 loading 与防重复提交 */
const submitting = ref(false)

/** 提交失败时展示的错误提示文本，空串表示无错误 */
const errorMessage = ref('')

/** 登录表单数据模型 */
const formState = reactive<LoginRequest>({
  username: '',
  password: '',
})

/**
 * 基础客户端校验（与后端 Req 1.2/1.9 一致），权威校验仍由后端负责。
 * @returns 校验通过返回 null，否则返回错误提示文本
 */
function validateForm(): string | null {
  const username = formState.username.trim()
  if (username.length < 3 || username.length > 32) {
    return '用户名长度需为 3 至 32 个字符'
  }
  if (formState.password.length < 8 || formState.password.length > 128) {
    return '密码长度需为 8 至 128 个字符'
  }
  return null
}

/**
 * 提交登录。
 * 校验通过后调用登录 API，成功写入会话并跳转，失败给出错误提示。
 */
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
    const token = await login({
      username: formState.username.trim(),
      password: formState.password,
    })
    // 写入会话 store（内部持久化到 localStorage，刷新后保持登录态）
    session.setToken(token)
    redirectAfterLogin()
  } catch (error) {
    // 登录失败（如凭证错误返回 401，Req 1.5）：提示用户，不跳转
    errorMessage.value =
      error instanceof Error ? error.message : '登录失败，请检查用户名或密码'
  } finally {
    submitting.value = false
  }
}

/**
 * 登录成功后的跳转目标。
 * 优先回跳到被路由守卫拦截前记录的 redirect 路径；缺省则进入 Dashboard。
 */
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
  <FullScreenLayout>
    <div class="relative flex min-h-screen w-full items-center justify-center bg-gray-50 p-6 dark:bg-gray-900">
      <div
        class="w-full max-w-md rounded-2xl border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-6 text-center">
          <h1 class="text-title-sm mb-2 font-semibold text-gray-800 dark:text-white/90">
            MCP Proxy Gateway 管理后台
          </h1>
          <p class="text-sm text-gray-500 dark:text-gray-400">请输入管理员凭证以登录</p>
        </div>

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
              class="flex h-11 w-full items-center justify-center rounded-lg bg-brand-500 text-sm font-medium text-white transition hover:bg-brand-600 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {{ submitting ? '登录中…' : '登录' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </FullScreenLayout>
</template>
