<script setup lang="ts">
/**
 * 常见问题：按「用户看到的现象」组织，而不是按内部模块。
 * 每条给出定位顺序而非单一答案，避免用户对着不匹配的答案反复试。
 */
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import { helpTopicRouteName, helpTopics } from '@/router/help'

interface FaqItem {
  question: string
  answer: string
  /** 延伸阅读的文档 id；标题从目录清单反查，避免两处各写一份 */
  topic?: string
}

/**
 * 由 id 反查文档标题。查不到（例如日后改了 id）返回 null，模板据此不渲染链接 ——
 * 比让 router-link 指向不存在的路由名直接抛错要好。
 */
function relatedTopic(id?: string): { name: string; title: string } | null {
  if (!id) return null
  const topic = helpTopics.find((item) => item.id === id)
  if (!topic) return null
  return { name: helpTopicRouteName(topic.id), title: topic.title }
}

const groups: { title: string; anchor: string; items: FaqItem[] }[] = [
  {
    title: '接入与连接',
    anchor: 'connect',
    items: [
      {
        question: '客户端连上了，但一个工具都看不到',
        answer:
          '依次确认四件事：这个 API Key 是否勾选了工具、工具在工具目录里是否被隐藏、对应上游是否处于已连接状态、上游本身是否真的注册了工具。四者是与的关系，任一不满足都会导致列表为空。',
        topic: 'apikeys',
      },
      {
        question: '上游一直连不上，错误是 initialize: EOF',
        answer:
          '这表示子进程启动后立刻退出了。常见原因是命令或包名不存在、缺少必需的环境变量、或该包在当前镜像里跑不起来。展开上游详情看最近错误原文，通常能直接定性。',
        topic: 'upstreams',
      },
      {
        question: '提示未找到可执行文件',
        answer:
          '镜像里没有这个命令。完整镜像自带 node、npx、python、python3、uv、uvx；其他工具需要自己放进运行时卷的 bin 目录并加可执行权限。如果用的是精简镜像，改用完整镜像更省事。',
        topic: 'runtime',
      },
      {
        question: '侧边栏没有脚本中心，运行环境里也没有依赖管理',
        answer:
          '当前跑的是精简镜像，它不含任何本地运行时，这些功能会自动隐藏。换成 :latest 或 :full 即可，数据卷可以直接复用。',
        topic: 'runtime',
      },
    ],
  },
  {
    title: '调用与权限',
    anchor: 'invoke',
    items: [
      {
        question: '返回 401，密钥明明是对的',
        answer:
          '先确认密钥没有被停用或删除。密钥明文无法找回，如果不能百分百确定手里这份是对的，重建一个更快。',
        topic: 'apikeys',
      },
      {
        question: '返回 429',
        answer:
          '触发了该密钥的速率限制或累计配额上限。到 API Key 管理调整限额，或等速率窗口重置。配额用完必须手动调额才会恢复。',
        topic: 'apikeys',
      },
      {
        question: '模型总是选错工具',
        answer:
          '先把用不到的工具隐藏掉，这一步通常比反复改描述更有效。剩下的工具把名字改成动词加对象的形式，描述里写清「什么时候该用它」。',
        topic: 'tools',
      },
      {
        question: '两个上游有同名工具，只显示一个',
        answer:
          '同名冲突需要主动处理。给其中一个起别名，或者用规则给整个上游加前缀，一次性解决所有重名。',
        topic: 'rules',
      },
    ],
  },
  {
    title: '运行环境与脚本',
    anchor: 'runtime',
    items: [
      {
        question: 'Python 上游报 ModuleNotFoundError',
        answer:
          '缺依赖。到「运行环境 → 依赖管理 → pip」装上对应包，装完即可被所有 Python 上游和脚本 import，不需要改环境变量。不要在上游启动参数里自行装包，严格档会拒绝。',
        topic: 'runtime',
      },
      {
        question: '升级镜像后 pip 依赖不见了',
        answer:
          '旧版把 pip 依赖装在 Python 虚拟环境里，新版改到独立目录且不再使用虚拟环境。旧依赖不会自动迁移，在依赖管理的 pip 页重装一次即可，之后再升级不会再有这个问题。',
        topic: 'runtime',
      },
      {
        question: '切到严格档之后上游起不来了',
        answer:
          '严格档要求显式声明工作目录与文件允许路径，npx / uvx 的目标包也必须在包白名单内。预检会指出缺哪一项。建议先在标准档下确认功能正常，再逐项收紧。',
        topic: 'security',
      },
    ],
  },
  {
    title: '排查方法',
    anchor: 'debug',
    items: [
      {
        question: '不知道该从哪里开始查',
        answer:
          '先去「调用记录」看这次请求有没有到网关。没有记录说明问题在客户端配置或网络；有记录但失败，就按记录里的错误原文往上游查。这个判断能省掉大部分无效尝试。',
        topic: 'observability',
      },
      {
        question: '昨天还正常，今天突然不行了',
        answer:
          '按时间范围翻「审计日志」，它记录了谁在什么时候改了哪个上游、密钥或规则。配置类问题这样找最快。',
        topic: 'observability',
      },
    ],
  },
]
</script>

<template>
  <HelpArticle
    title="常见问题"
    subtitle="按你实际看到的现象查找。每条给的是定位顺序，照着走比直接套答案更可靠。"
  >
    <HelpCallout tone="info" title="通用定位思路">
      问题一定在客户端、网关、上游三者之一。「调用记录」能一眼分清是哪一层，
      先做这个判断，再决定查什么。
    </HelpCallout>

    <HelpSection
      v-for="group in groups"
      :key="group.anchor"
      :title="group.title"
      :anchor="group.anchor"
    >
      <div class="space-y-3">
        <details
          v-for="item in group.items"
          :key="item.question"
          class="group/faq open:border-brand-200 dark:open:border-brand-500/30 rounded-xl border border-gray-200 bg-white px-4 py-3 transition-colors dark:border-gray-800 dark:bg-white/[0.02]"
        >
          <summary
            class="flex cursor-pointer list-none items-center justify-between gap-3 text-sm font-medium text-gray-800 marker:hidden dark:text-white/90"
          >
            <span>{{ item.question }}</span>
            <span
              class="text-gray-400 transition-transform duration-200 group-open/faq:rotate-45 motion-reduce:transition-none dark:text-gray-500"
              aria-hidden="true"
              >+</span
            >
          </summary>
          <p class="mt-3 text-sm leading-7 text-gray-600 dark:text-gray-300">{{ item.answer }}</p>
          <router-link
            v-if="relatedTopic(item.topic)"
            :to="{ name: relatedTopic(item.topic)!.name }"
            class="text-brand-600 dark:text-brand-300 mt-2 inline-block text-sm hover:underline"
          >
            详见：{{ relatedTopic(item.topic)!.title }}
          </router-link>
        </details>
      </div>
    </HelpSection>
  </HelpArticle>
</template>
