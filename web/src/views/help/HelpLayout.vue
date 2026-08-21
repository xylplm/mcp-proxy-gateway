<script setup lang="ts">
/**
 * 帮助中心布局壳。
 *
 * 独立于 AdminLayout：不依赖侧边栏 / 会话 / 运行时能力等控制台状态，
 * 因此可在新窗口单独打开，也不会把管理台代码带进这个分包。
 *
 * 目录、搜索、上一篇/下一篇都由 router/help.ts 的清单驱动，新增文档零改动。
 */
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ThemeToggler from '@/components/common/ThemeToggler.vue'
import { ChevronRightIcon, HelpCircleIcon } from '@/icons'
import {
  HELP_HOME_ROUTE,
  helpTopicGroups,
  helpTopicRouteName,
  helpTopics,
  type HelpTopic,
} from '@/router/help'

const route = useRoute()

const keyword = ref('')
/** 小屏目录抽屉；≥lg 时目录常驻，该状态不参与布局。 */
const navOpen = ref(false)

/** 搜索命中：标题、摘要、关键词任一包含即算。空关键词返回完整分组。 */
const filteredGroups = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (kw === '') return helpTopicGroups
  return helpTopicGroups
    .map((group) => ({
      title: group.title,
      topics: group.topics.filter((topic) => matchTopic(topic, kw)),
    }))
    .filter((group) => group.topics.length > 0)
})

function matchTopic(topic: HelpTopic, kw: string): boolean {
  if (topic.title.toLowerCase().includes(kw)) return true
  if (topic.summary.toLowerCase().includes(kw)) return true
  return topic.keywords.some((k) => k.toLowerCase().includes(kw))
}

const hasResult = computed(() => filteredGroups.value.length > 0)

/** 当前文档在扁平清单中的位置，用于底部上一篇/下一篇。 */
const currentIndex = computed(() =>
  helpTopics.findIndex((topic) => route.name === helpTopicRouteName(topic.id)),
)
const prevTopic = computed(() =>
  currentIndex.value > 0 ? helpTopics[currentIndex.value - 1] : null,
)
const nextTopic = computed(() =>
  currentIndex.value >= 0 && currentIndex.value < helpTopics.length - 1
    ? helpTopics[currentIndex.value + 1]
    : null,
)

function isCurrent(topic: HelpTopic): boolean {
  return route.name === helpTopicRouteName(topic.id)
}

// 移动端点开目录后跳转应自动收起，否则内容被抽屉挡住。
watch(
  () => route.fullPath,
  () => {
    navOpen.value = false
  },
)
</script>

<template>
  <div class="min-h-screen bg-gray-50 dark:bg-gray-950">
    <!-- 顶栏：品牌 + 搜索 + 返回控制台 -->
    <header
      class="sticky top-0 z-40 border-b border-gray-200 bg-white/85 backdrop-blur-md dark:border-gray-800 dark:bg-gray-900/85"
    >
      <div class="mx-auto flex h-16 max-w-[1600px] items-center gap-3 px-4 sm:gap-4 sm:px-6">
        <router-link
          :to="{ name: HELP_HOME_ROUTE }"
          class="flex min-w-0 items-center gap-2.5"
          aria-label="帮助中心首页"
        >
          <span
            class="from-brand-500 to-brand-600 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br text-white shadow-sm"
          >
            <HelpCircleIcon class="h-5 w-5" aria-hidden="true" />
          </span>
          <span class="min-w-0">
            <span
              class="block truncate text-sm font-semibold text-gray-800 sm:text-base dark:text-white/90"
              >帮助中心</span
            >
            <span class="hidden text-xs text-gray-400 sm:block dark:text-gray-500"
              >MCP Proxy Gateway</span
            >
          </span>
        </router-link>

        <div class="ml-auto flex items-center gap-2 sm:gap-3">
          <label class="relative hidden md:block">
            <span class="sr-only">搜索帮助文档</span>
            <input
              v-model="keyword"
              type="search"
              placeholder="搜索功能、报错或关键词"
              class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-56 rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 placeholder:text-gray-400 focus:ring-3 focus:outline-none lg:w-72 dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30"
            />
          </label>
          <ThemeToggler />
          <router-link
            to="/"
            class="bg-brand-500 hover:bg-brand-600 hidden h-10 items-center rounded-lg px-4 text-sm font-medium text-white transition-colors sm:inline-flex"
          >
            返回控制台
          </router-link>
          <button
            type="button"
            class="inline-flex h-10 items-center rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 lg:hidden dark:border-gray-700 dark:text-gray-300"
            :aria-expanded="navOpen"
            @click="navOpen = !navOpen"
          >
            {{ navOpen ? '收起目录' : '目录' }}
          </button>
        </div>
      </div>
    </header>

    <div class="mx-auto flex max-w-[1600px] gap-8 px-4 py-6 sm:px-6 lg:py-10">
      <!-- 目录：≥lg 常驻侧栏，小屏为可折叠面板 -->
      <aside
        class="shrink-0 lg:sticky lg:top-24 lg:block lg:h-[calc(100vh-8rem)] lg:w-64 lg:overflow-y-auto xl:w-72"
        :class="navOpen ? 'fixed inset-x-4 top-20 z-30 max-h-[70vh] overflow-y-auto' : 'hidden'"
      >
        <div
          class="rounded-2xl border border-gray-200 bg-white p-3 shadow-sm lg:border-0 lg:bg-transparent lg:p-0 lg:shadow-none dark:border-gray-800 dark:bg-gray-900 lg:dark:bg-transparent"
        >
          <label class="relative mb-3 block md:hidden">
            <span class="sr-only">搜索帮助文档</span>
            <input
              v-model="keyword"
              type="search"
              placeholder="搜索功能或报错"
              class="focus:border-brand-300 h-10 w-full rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 placeholder:text-gray-400 focus:outline-none dark:border-gray-700 dark:text-white/90"
            />
          </label>

          <router-link
            :to="{ name: HELP_HOME_ROUTE }"
            class="mb-2 block rounded-lg px-3 py-2 text-sm font-medium transition-colors"
            :class="
              route.name === HELP_HOME_ROUTE
                ? 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
                : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-white/5'
            "
          >
            快速上手
          </router-link>

          <nav v-if="hasResult" class="space-y-5">
            <div v-for="group in filteredGroups" :key="group.title">
              <p
                class="px-3 pb-1.5 text-[11px] font-semibold tracking-wider text-gray-400 uppercase dark:text-gray-500"
              >
                {{ group.title }}
              </p>
              <ul class="space-y-0.5">
                <li v-for="topic in group.topics" :key="topic.id">
                  <router-link
                    :to="{ name: helpTopicRouteName(topic.id) }"
                    class="relative block rounded-lg py-2 pr-3 pl-3 text-sm transition-colors"
                    :class="
                      isCurrent(topic)
                        ? 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300 font-medium'
                        : 'text-gray-600 hover:bg-gray-100 hover:text-gray-800 dark:text-gray-300 dark:hover:bg-white/5 dark:hover:text-white'
                    "
                  >
                    <span
                      v-if="isCurrent(topic)"
                      class="bg-brand-500 absolute top-1/2 left-0 h-5 w-0.5 -translate-y-1/2 rounded-full"
                      aria-hidden="true"
                    ></span>
                    {{ topic.title }}
                  </router-link>
                </li>
              </ul>
            </div>
          </nav>
          <p v-else class="px-3 py-6 text-sm text-gray-400 dark:text-gray-500">
            没有匹配「{{ keyword }}」的文档，试试更短的关键词。
          </p>
        </div>
      </aside>

      <!-- 内容区：切换文档时淡入上移，尊重系统的减弱动效设置 -->
      <main class="min-w-0 flex-1">
        <!-- 文档组件由路由懒加载，vue-router 会在导航完成前解析好，因此这里不需要
             Suspense 占位；切换时的连续感交给过渡动画。 -->
        <router-view v-slot="{ Component, route: current }">
          <transition
            enter-active-class="transition duration-300 ease-out motion-reduce:transition-none"
            enter-from-class="opacity-0 translate-y-2"
            enter-to-class="opacity-100 translate-y-0"
            mode="out-in"
          >
            <component :is="Component" :key="current.fullPath" />
          </transition>
        </router-view>

        <!-- 上一篇 / 下一篇：让用户能顺序读完，不必回目录 -->
        <nav
          v-if="prevTopic || nextTopic"
          class="mt-8 grid gap-3 sm:grid-cols-2"
          aria-label="文档翻页"
        >
          <router-link
            v-if="prevTopic"
            :to="{ name: helpTopicRouteName(prevTopic.id) }"
            class="hover:border-brand-300 dark:hover:border-brand-500/40 group rounded-xl border border-gray-200 bg-white p-4 transition hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
          >
            <span class="text-xs text-gray-400 dark:text-gray-500">上一篇</span>
            <span
              class="group-hover:text-brand-600 dark:group-hover:text-brand-300 mt-1 block text-sm font-medium text-gray-800 dark:text-white/90"
            >
              {{ prevTopic.title }}
            </span>
          </router-link>
          <span v-else class="hidden sm:block"></span>
          <router-link
            v-if="nextTopic"
            :to="{ name: helpTopicRouteName(nextTopic.id) }"
            class="hover:border-brand-300 dark:hover:border-brand-500/40 group rounded-xl border border-gray-200 bg-white p-4 transition hover:shadow-md sm:text-right dark:border-gray-800 dark:bg-white/[0.03]"
          >
            <span class="text-xs text-gray-400 dark:text-gray-500">下一篇</span>
            <span
              class="group-hover:text-brand-600 dark:group-hover:text-brand-300 mt-1 flex items-center gap-1 text-sm font-medium text-gray-800 sm:justify-end dark:text-white/90"
            >
              {{ nextTopic.title }}
              <ChevronRightIcon class="h-4 w-4" aria-hidden="true" />
            </span>
          </router-link>
        </nav>
      </main>
    </div>
  </div>
</template>
