<template>
  <aside
    :class="[
      'fixed top-0 left-0 z-99999 mt-16 flex h-[calc(100dvh-4rem)] flex-col border-r border-gray-200 bg-white px-5 text-gray-900 transition-all duration-300 ease-in-out lg:mt-0 lg:h-screen dark:border-gray-800 dark:bg-gray-900',
      {
        'lg:w-[290px]': isExpanded || isMobileOpen || isHovered,
        'lg:w-[90px]': !isExpanded && !isHovered,
        'w-[min(290px,calc(100vw_-_24px))] translate-x-0': isMobileOpen,
        '-translate-x-full': !isMobileOpen,
        'lg:translate-x-0': true,
      },
    ]"
    @mouseenter="!isExpanded && (isHovered = true)"
    @mouseleave="isHovered = false"
  >
    <div :class="['flex py-8', !isExpanded && !isHovered ? 'lg:justify-center' : 'justify-start']">
      <router-link to="/" @click="closeMobileSidebar">
        <img
          v-if="isExpanded || isHovered || isMobileOpen"
          class="dark:hidden"
          src="/images/logo/logo.svg"
          alt="Logo"
          width="150"
          height="40"
        />
        <img
          v-if="isExpanded || isHovered || isMobileOpen"
          class="hidden dark:block"
          src="/images/logo/logo-dark.svg"
          alt="Logo"
          width="150"
          height="40"
        />
        <img v-else src="/images/logo/logo-icon.svg" alt="Logo" width="32" height="32" />
      </router-link>
    </div>
    <div class="no-scrollbar flex flex-col overflow-y-auto duration-300 ease-linear">
      <nav class="mb-6">
        <div class="flex flex-col gap-4">
          <div v-for="(menuGroup, groupIndex) in menuGroups" :key="groupIndex">
            <h2
              :class="[
                'mb-4 flex text-xs leading-[20px] text-gray-400 uppercase',
                !isExpanded && !isHovered ? 'lg:justify-center' : 'justify-start',
              ]"
            >
              <template v-if="isExpanded || isHovered || isMobileOpen">
                {{ menuGroup.title }}
              </template>
              <HorizontalDots v-else />
            </h2>
            <ul class="flex flex-col gap-4">
              <li v-for="item in menuGroup.items" :key="item.name">
                <router-link
                  v-if="item.path"
                  :to="item.path"
                  :class="[
                    'menu-item group',
                    {
                      'menu-item-active': isActive(item.path),
                      'menu-item-inactive': !isActive(item.path),
                    },
                  ]"
                  @click="closeMobileSidebar"
                >
                  <span
                    :class="[
                      'shrink-0',
                      isActive(item.path) ? 'menu-item-icon-active' : 'menu-item-icon-inactive',
                    ]"
                  >
                    <component :is="item.icon" />
                  </span>
                  <span v-if="isExpanded || isHovered || isMobileOpen" class="menu-item-text min-w-0 flex-1 truncate whitespace-nowrap">{{
                    item.name
                  }}</span>
                </router-link>
              </li>
            </ul>
          </div>
        </div>
      </nav>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { useRoute } from 'vue-router'
import {
  GridIcon,
  HorizontalDots,
  PlugInIcon,
  ListIcon,
  TableIcon,
  SettingsIcon,
  PieChartIcon,
  DocsIcon,
  ErrorHexaIcon,
  FlagIcon,
  Message2Line,
  InfoCircleIcon,
  UserGroupIcon,
  SendIcon,
} from '@/icons'
import { useSidebar } from '@/composables/useSidebar'

const route = useRoute()

const { isExpanded, isMobileOpen, isHovered, closeMobileSidebar } = useSidebar()

const menuGroups = [
  {
    title: '总览',
    items: [
      {
        icon: GridIcon,
        name: '仪表盘',
        path: '/',
      },
      {
        icon: PieChartIcon,
        name: '调用统计',
        path: '/statistics',
      },
    ],
  },
  {
    title: 'MCP 管理',
    items: [
      {
        icon: PlugInIcon,
        name: '上游 MCP 管理',
        path: '/upstreams',
      },
      {
        icon: TableIcon,
        name: '工具目录',
        path: '/tools',
      },
      {
        icon: ListIcon,
        name: '规则管理',
        path: '/rules',
      },
    ],
  },
  {
    title: 'API 管理',
    items: [
      {
        icon: UserGroupIcon,
        name: 'API Key 管理',
        path: '/apikeys',
      },
      {
        icon: SendIcon,
        name: 'API 服务',
        path: '/api-service',
      },
      {
        icon: DocsIcon,
        name: '调用记录',
        path: '/call-records',
      },
    ],
  },
  {
    title: '系统',
    items: [
      {
        icon: SettingsIcon,
        name: '系统设置',
        path: '/settings',
      },
      {
        icon: ErrorHexaIcon,
        name: '安全中心',
        path: '/security',
      },
      {
        icon: FlagIcon,
        name: '审计日志',
        path: '/audit',
      },
      {
        icon: Message2Line,
        name: '系统日志',
        path: '/system-logs',
      },
      {
        icon: InfoCircleIcon,
        name: '关于',
        path: '/about',
      },
    ],
  },
]

const isActive = (path: string) => {
  if (path === '/') return route.path === '/'
  return route.path === path || route.path.startsWith(`${path}/`)
}
</script>
