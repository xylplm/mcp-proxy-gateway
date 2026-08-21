<script setup lang="ts">
/**
 * 帮助中心首页：先用三步把人送到「能跑通」，再给按模块的入口网格。
 * 卡片直接复用 router/help.ts 的清单，新增文档会自动出现在这里。
 */
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'
import { helpTopicGroups, helpTopicRouteName } from '@/router/help'

const quickStart: HelpStep[] = [
  {
    title: '接入一个上游 MCP',
    desc: '到「上游 MCP 管理」新建。不确定填什么就先开模板市场，挑一个内置服务一键预填。',
    hint: '远程服务（SSE / HTTP）只需要一个地址，最省事；本地 stdio 需要镜像里有对应运行时。',
  },
  {
    title: '确认工具已聚合进来',
    desc: '到「工具目录」看这个上游的工具是否出现。名字重复或不想暴露的，可以改别名或隐藏。',
    hint: '上游状态是「已连接」但没有工具，通常是上游自身没有注册工具。',
  },
  {
    title: '创建 API Key 并接入客户端',
    desc: '到「API Key 管理」建一个密钥，按需勾选它能用的工具，再到「API 服务」复制端点地址与配置片段。',
    hint: '密钥明文只在创建时显示一次，记得当场复制保存。',
  },
]
</script>

<template>
  <div class="space-y-6 sm:space-y-8">
    <!-- 首屏引导：说清网关是干什么的，避免用户从概念开始猜 -->
    <section
      class="from-brand-500 via-brand-600 to-brand-700 relative overflow-hidden rounded-2xl bg-gradient-to-br p-6 text-white shadow-sm sm:p-8"
    >
      <div
        class="pointer-events-none absolute -top-16 -right-16 h-56 w-56 rounded-full bg-white/10 blur-2xl"
        aria-hidden="true"
      ></div>
      <div class="relative max-w-3xl">
        <p class="text-xs font-medium tracking-wider text-white/70 uppercase">快速上手</p>
        <h1 class="mt-2 text-2xl font-semibold sm:text-3xl">三步跑通第一个工具调用</h1>
        <p class="mt-3 text-sm leading-7 text-white/85 sm:text-base">
          网关把多个 MCP
          服务聚合成一个入口：上游接进来，工具统一改名与授权，客户端只连网关一个地址。
          下面三步做完就能在客户端里调到工具。
        </p>
      </div>
    </section>

    <section
      class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm sm:p-7 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <HelpSteps :steps="quickStart" />
      <HelpCallout tone="success" title="只想先看看效果？" class="mt-6">
        模板市场里的远程服务不需要任何本地运行时，接进来就能连通，适合第一次试。
      </HelpCallout>
    </section>

    <!-- 模块入口：按控制台的功能分组，用户按自己要做的事找文档 -->
    <section v-for="group in helpTopicGroups" :key="group.title">
      <h2 class="mb-3 text-base font-semibold text-gray-800 dark:text-white/90">
        {{ group.title }}
      </h2>
      <div class="3xl:grid-cols-3 grid gap-3 sm:grid-cols-2">
        <router-link
          v-for="topic in group.topics"
          :key="topic.id"
          :to="{ name: helpTopicRouteName(topic.id) }"
          class="hover:border-brand-300 dark:hover:border-brand-500/40 group/card rounded-xl border border-gray-200 bg-white p-4 transition duration-300 hover:-translate-y-0.5 hover:shadow-md motion-reduce:transform-none motion-reduce:transition-none dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <p
            class="group-hover/card:text-brand-600 dark:group-hover/card:text-brand-300 font-medium text-gray-800 dark:text-white/90"
          >
            {{ topic.title }}
          </p>
          <p class="mt-1.5 text-sm leading-6 text-gray-500 dark:text-gray-400">
            {{ topic.summary }}
          </p>
        </router-link>
      </div>
    </section>
  </div>
</template>
