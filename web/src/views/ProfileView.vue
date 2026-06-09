<script setup lang="ts">
/**
 * 个人中心 / 账户页。
 *
 * 本页展示当前管理员账户信息，并承接账户密码修改。
 *
 * 容错：加载用户名失败时给出提示并支持重试；若 session 已缓存用户名则直接展示。
 */
import { onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { UserCircleIcon } from '@/icons'
import { useSessionStore } from '@/stores/session'
import { getCurrentAdmin } from '@/api/auth'
import { changePassword, extractAPIError } from '@/api/settings'

const session = useSessionStore()

const username = ref(session.username ?? '')
const loading = ref(false)
const loadError = ref('')
const toast = ref('')

const pwd = reactive({ currentPassword: '', newPassword: '', confirmPassword: '' })
const pwdSaving = ref(false)
const pwdError = ref('')
const pwdFieldErrors = reactive<Record<string, string>>({})

/** 头像首字母（用户名首字符大写）；无用户名时为空，回退图标。 */
function initialOf(name: string): string {
  return name.trim().length > 0 ? name.trim().charAt(0).toUpperCase() : ''
}

function errorMessage(err: unknown): string {
  // 请求层已将失败统一为 Error（ApiError），其 message 即后端中文提示。
  return err instanceof Error ? err.message : '加载账户信息失败，请稍后重试'
}

async function loadProfile(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const me = await getCurrentAdmin()
    username.value = me.username
    session.setUsername(me.username)
  } catch (err) {
    loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

onMounted(loadProfile)

function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

function clearPwdErrors(): void {
  pwdError.value = ''
  for (const k of Object.keys(pwdFieldErrors)) {
    delete pwdFieldErrors[k]
  }
}

async function submitChangePassword(): Promise<void> {
  if (pwdSaving.value) return
  clearPwdErrors()

  if (pwd.newPassword.length < 6 || pwd.newPassword.length > 128) {
    pwdFieldErrors.newPassword = '新密码长度需在 6 至 128 个字符之间'
    return
  }
  if (pwd.newPassword !== pwd.confirmPassword) {
    pwdFieldErrors.confirmPassword = '两次输入的新密码不一致'
    return
  }

  pwdSaving.value = true
  try {
    await changePassword({
      currentPassword: pwd.currentPassword,
      newPassword: pwd.newPassword,
    })
    pwd.currentPassword = ''
    pwd.newPassword = ''
    pwd.confirmPassword = ''
    showToast('密码已更新')
  } catch (err) {
    const body = extractAPIError(err)
    if (body?.fields) {
      for (const [k, v] of Object.entries(body.fields)) {
        pwdFieldErrors[k] = v
      }
    }
    pwdError.value =
      body?.message ?? (err instanceof Error ? err.message : '改密失败，请稍后重试')
  } finally {
    pwdSaving.value = false
  }
}

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
const hintClass = 'mt-1 text-xs text-gray-400 dark:text-gray-500'
const errClass = 'mt-1 text-xs text-error-500'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="个人中心" />

    <p
      v-if="toast !== ''"
      class="mb-4 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
    >
      {{ toast }}
    </p>

    <div
      v-if="loadError !== ''"
      class="mb-6 flex items-center justify-between rounded-lg bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      <span>{{ loadError }}</span>
      <button
        type="button"
        class="rounded-md border border-error-200 px-3 py-1 text-xs font-medium hover:bg-error-100 dark:border-error-500/30 dark:hover:bg-error-500/10"
        @click="loadProfile"
      >
        重试
      </button>
    </div>

    <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
      <!-- 账户信息 -->
      <section :class="cardClass">
        <div class="flex items-start justify-between gap-4">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">账户信息</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">当前登录的管理账户</p>
          </div>
          <span
            class="rounded-full bg-success-50 px-2.5 py-1 text-xs font-medium text-success-700 dark:bg-success-500/10 dark:text-success-400"
          >
            已登录
          </span>
        </div>

        <div class="mt-6 flex items-center gap-4">
          <span
            class="flex h-16 w-16 items-center justify-center overflow-hidden rounded-full bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
          >
            <span v-if="initialOf(username) !== ''" class="text-xl font-semibold">
              {{ initialOf(username) }}
            </span>
            <UserCircleIcon v-else class="h-9 w-9" />
          </span>
          <div>
            <p class="text-lg font-medium text-gray-800 dark:text-white/90">
              <span v-if="loading && username === ''" class="text-gray-400 dark:text-gray-500">
                加载中…
              </span>
              <span v-else>{{ username || '未知用户' }}</span>
            </p>
            <p class="mt-0.5 text-sm text-gray-500 dark:text-gray-400">系统管理员</p>
          </div>
        </div>

        <dl
          class="mt-6 grid grid-cols-1 gap-4 border-t border-gray-100 pt-6 sm:grid-cols-2 xl:grid-cols-1 dark:border-gray-800"
        >
          <div>
            <dt class="text-xs text-gray-400 dark:text-gray-500">用户名</dt>
            <dd class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              {{ username || '—' }}
            </dd>
          </div>
          <div>
            <dt class="text-xs text-gray-400 dark:text-gray-500">角色</dt>
            <dd class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">管理员</dd>
          </div>
        </dl>
      </section>

      <!-- 安全设置 -->
      <section :class="cardClass">
        <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">安全设置</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">更新当前管理员账户密码</p>
          </div>
        </div>

        <p
          v-if="pwdError !== ''"
          class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ pwdError }}
        </p>

        <form class="flex flex-col gap-5" @submit.prevent="submitChangePassword">
          <div class="grid grid-cols-1 gap-5 md:grid-cols-2">
            <div class="md:col-span-2">
              <label :class="labelClass">当前密码</label>
              <input
                v-model="pwd.currentPassword"
                type="password"
                autocomplete="current-password"
                :class="inputClass"
              />
              <p v-if="pwdFieldErrors['currentPassword']" :class="errClass">
                {{ pwdFieldErrors['currentPassword'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">新密码</label>
              <input
                v-model="pwd.newPassword"
                type="password"
                autocomplete="new-password"
                :class="inputClass"
              />
              <p :class="hintClass">长度 6 至 128 个字符。</p>
              <p v-if="pwdFieldErrors['newPassword']" :class="errClass">
                {{ pwdFieldErrors['newPassword'] }}
              </p>
            </div>
            <div>
              <label :class="labelClass">确认新密码</label>
              <input
                v-model="pwd.confirmPassword"
                type="password"
                autocomplete="new-password"
                :class="inputClass"
              />
              <p v-if="pwdFieldErrors['confirmPassword']" :class="errClass">
                {{ pwdFieldErrors['confirmPassword'] }}
              </p>
            </div>
          </div>

          <div class="flex justify-end">
            <button
              type="submit"
              class="rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
              :disabled="pwdSaving"
            >
              {{ pwdSaving ? '提交中…' : '修改密码' }}
            </button>
          </div>
        </form>
      </section>
    </div>
  </AdminLayout>
</template>
