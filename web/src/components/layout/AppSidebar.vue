<template>
  <aside
    :class="[
      'fixed top-0 left-0 z-99999 mt-16 flex h-screen flex-col border-r border-gray-200 bg-white px-5 text-gray-900 transition-all duration-300 ease-in-out lg:mt-0 dark:border-gray-800 dark:bg-gray-900',
      {
        'lg:w-[290px]': isExpanded || isMobileOpen || isHovered,
        'lg:w-[90px]': !isExpanded && !isHovered,
        'w-[290px] translate-x-0': isMobileOpen,
        '-translate-x-full': !isMobileOpen,
        'lg:translate-x-0': true,
      },
    ]"
    @mouseenter="!isExpanded && (isHovered = true)"
    @mouseleave="isHovered = false"
  >
    <div :class="['flex py-8', !isExpanded && !isHovered ? 'lg:justify-center' : 'justify-start']">
      <router-link to="/">
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
                >
                  <span
                    :class="[
                      isActive(item.path) ? 'menu-item-icon-active' : 'menu-item-icon-inactive',
                    ]"
                  >
                    <component :is="item.icon" />
                  </span>
                  <span v-if="isExpanded || isHovered || isMobileOpen" class="menu-item-text">{{
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
import { GridIcon, HorizontalDots, PlugInIcon, ListIcon, BoxCubeIcon, SettingsIcon, PieChartIcon, TableIcon } from '@/icons'
import { useSidebar } from '@/composables/useSidebar'

const route = useRoute()

const { isExpanded, isMobileOpen, isHovered } = useSidebar()

const menuGroups = [
  {
    title: 'Menu',
    items: [
      {
        icon: GridIcon,
        name: 'Dashboard',
        path: '/',
      },
      {
        icon: PlugInIcon,
        name: '上游 MCP 管理',
        path: '/upstreams',
      },
      {
        icon: ListIcon,
        name: '规则管理',
        path: '/rules',
      },
      {
        icon: BoxCubeIcon,
        name: 'API Key 管理',
        path: '/apikeys',
      },
      {
        icon: PieChartIcon,
        name: '调用统计',
        path: '/statistics',
      },
      {
        icon: TableIcon,
        name: '审计日志',
        path: '/audit',
      },
      {
        icon: SettingsIcon,
        name: '系统设置',
        path: '/settings',
      },
    ],
  },
]

const isActive = (path: string) => route.path === path
</script>
