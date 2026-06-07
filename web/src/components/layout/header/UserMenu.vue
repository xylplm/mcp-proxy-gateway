<template>
  <div class="relative" ref="dropdownRef">
    <button
      class="flex items-center text-gray-700 dark:text-gray-400"
      @click.prevent="toggleDropdown"
    >
      <!-- 初始/图标头像，不依赖 /images/user/*.jpg -->
      <span
        class="mr-3 flex h-11 w-11 items-center justify-center overflow-hidden rounded-full bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
      >
        <span v-if="initial !== ''" class="text-sm font-semibold">{{ initial }}</span>
        <UserCircleIcon v-else class="h-6 w-6" />
      </span>

      <span class="text-theme-sm mr-1 block font-medium">{{ displayName }}</span>

      <ChevronDownIcon :class="{ 'rotate-180': dropdownOpen }" />
    </button>

    <!-- Dropdown Start -->
    <div
      v-if="dropdownOpen"
      class="shadow-theme-lg dark:bg-gray-dark absolute right-0 mt-[17px] flex w-[260px] flex-col rounded-2xl border border-gray-200 bg-white p-3 dark:border-gray-800"
    >
      <div>
        <span class="text-theme-sm block font-medium text-gray-700 dark:text-gray-400">
          {{ displayName }}
        </span>
        <span class="text-theme-xs mt-0.5 block text-gray-500 dark:text-gray-400">
          管理员
        </span>
      </div>

      <ul class="flex flex-col gap-1 border-b border-gray-200 pt-4 pb-3 dark:border-gray-800">
        <li v-for="item in menuItems" :key="item.href">
          <router-link
            :to="item.href"
            @click="closeDropdown"
            class="group text-theme-sm flex items-center gap-3 rounded-lg px-3 py-2 font-medium text-gray-700 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-white/5 dark:hover:text-gray-300"
          >
            <component
              :is="item.icon"
              class="text-gray-500 group-hover:text-gray-700 dark:group-hover:text-gray-300"
            />
            {{ item.text }}
          </router-link>
        </li>
      </ul>
      <button
        type="button"
        @click="signOut"
        class="group text-theme-sm mt-3 flex w-full items-center gap-3 rounded-lg px-3 py-2 font-medium text-gray-700 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-white/5 dark:hover:text-gray-300"
      >
        <LogoutIcon
          class="text-gray-500 group-hover:text-gray-700 dark:group-hover:text-gray-300"
        />
        退出登录
      </button>
    </div>
    <!-- Dropdown End -->
  </div>
</template>

<script setup lang="ts">
import { UserCircleIcon, ChevronDownIcon, LogoutIcon, SettingsIcon } from '@/icons'
import { RouterLink, useRouter } from 'vue-router'
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import { getCurrentAdmin } from '@/api/auth'

const router = useRouter()
const session = useSessionStore()

const dropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)

/** 下拉菜单项：账户中心、系统设置（退出登录单独处理）。 */
const menuItems = [
  { href: '/profile', icon: UserCircleIcon, text: '个人中心 / 账户' },
  { href: '/settings', icon: SettingsIcon, text: '系统设置' },
]

/** 展示名：优先真实用户名，未取到时回退占位。 */
const displayName = computed(() => session.username ?? '管理员')

/** 头像首字母（取用户名首个非空字符并大写）；无用户名时返回空串以回退图标。 */
const initial = computed(() => {
  const name = session.username ?? ''
  return name.trim().length > 0 ? name.trim().charAt(0).toUpperCase() : ''
})

const toggleDropdown = () => {
  dropdownOpen.value = !dropdownOpen.value
}

const closeDropdown = () => {
  dropdownOpen.value = false
}

/** 退出登录：清除会话并跳转登录页。 */
const signOut = () => {
  closeDropdown()
  session.clearSession()
  void router.push({ name: 'login' })
}

const handleClickOutside = (event: MouseEvent) => {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    closeDropdown()
  }
}

onMounted(async () => {
  document.addEventListener('click', handleClickOutside)
  // 按需拉取当前管理员用户名（未缓存时），失败静默回退占位名。
  if (session.username === null) {
    try {
      const me = await getCurrentAdmin()
      session.setUsername(me.username)
    } catch {
      // 忽略：拉取失败时使用占位展示名，不阻塞页面。
    }
  }
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>
