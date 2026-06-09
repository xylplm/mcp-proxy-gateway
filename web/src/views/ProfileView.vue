<script setup lang="ts">
/**
 * 个人中心 / 账户页。
 *
 * 设计选择：保持最小且不与设置页重复。本页仅展示当前管理员账户信息
 * （用户名，来自 GET /api/admin/auth/me），密码修改不在此重复实现，
 * 而是引导至系统设置页（/settings）已有的「修改密码」区块。这样避免两处
 * 维护同一份改密表单（复用 changePassword 的逻辑已集中在 SettingsView）。
 *
 * 容错：加载用户名失败时给出提示并支持重试；若 session 已缓存用户名则直接展示。
 */
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { UserCircleIcon, SettingsIcon } from '@/icons'
import { useSessionStore } from '@/stores/session'
import { getCurrentAdmin } from '@/api/auth'

const session = useSessionStore()

const username = ref(session.username ?? '')
const loading = ref(false)
const loadError = ref('')

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

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="个人中心" />

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

    <!-- 账户信息 -->
    <section :class="cardClass">
      <h3 class="mb-5 text-base font-semibold text-gray-800 dark:text-white/90">账户信息</h3>
      <div class="flex items-center gap-4">
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

      <dl class="mt-6 grid grid-cols-1 gap-4 border-t border-gray-100 pt-6 sm:grid-cols-2 dark:border-gray-800">
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

    <!-- 安全设置：引导至系统设置改密，避免与设置页表单重复 -->
    <section :class="cardClass" class="mt-6">
      <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">安全设置</h3>
      <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
        修改管理员密码请前往系统设置页的「修改密码」区块。
      </p>
      <router-link
        to="/settings"
        class="group inline-flex items-center gap-3 rounded-lg border border-gray-200 px-4 py-3 text-sm font-medium text-gray-700 transition hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]"
      >
        <SettingsIcon
          class="text-gray-500 group-hover:text-brand-600 dark:group-hover:text-brand-400"
        />
        前往系统设置修改密码
      </router-link>
    </section>
  </AdminLayout>
</template>
