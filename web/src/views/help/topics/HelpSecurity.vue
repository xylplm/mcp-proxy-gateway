<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const modes: HelpField[] = [
  {
    name: '标准',
    tag: '默认',
    desc: '命令白名单 + 敏感环境变量清理 + 独立进程组。适合大多数场景，也是调试新上游时的推荐起点。',
  },
  {
    name: '严格',
    desc: '在标准之上：命令收窄到更小的白名单，npx / uvx 的目标包必须在包白名单内，强制声明工作目录与文件允许路径，可执行文件只接受受信运行时目录内的。Linux 上有 bubblewrap 时还会启用文件绑定隔离，网络设为拒绝时真正断网。',
  },
  {
    name: '完全放行',
    tag: '谨慎',
    desc: '只保留最基本的黑名单（仍禁 shell 类命令）。管理台会显示醒目告警并要求二次确认。只在明确知道后果时使用。',
  },
]

const strictItems: HelpField[] = [
  {
    name: '受信运行时目录',
    desc: '严格档只接受运行时卷与镜像内置解释器目录里的可执行文件，系统 PATH 上的其他位置一律拒绝，符号链接越界也拒绝。',
  },
  {
    name: '包白名单',
    desc: '严格档不禁止 npx / uvx，但它们要跑的包必须在白名单内。默认已包含官方模板常用的包，支持 @scope/* 前缀。',
  },
  {
    name: '文件允许路径',
    desc: '子进程只能访问声明过的目录。有 bubblewrap 时是内核级绑定挂载，没有时退化为策略校验。',
  },
  {
    name: '网络策略',
    desc: 'allowlist 是协作性声明；deny 在 Linux 上会创建独立网络命名空间，真正断网。',
  },
]

const rollout: HelpStep[] = [
  {
    title: '先用标准档跑通',
    desc: '新上游先在标准档下确认功能正常。一上来就用严格档，很容易把功能问题误判成安全策略问题。',
  },
  {
    title: '切到严格档，补齐声明',
    desc: '切换后按报错逐项补：工作目录、文件允许路径、包白名单。预检会明确告诉你缺哪一项。',
  },
  {
    title: '收紧网络',
    desc: '确认这个上游到底要不要出网。不需要就设为拒绝，这是收益最大的一步。',
  },
  {
    title: '在安全中心复核',
    desc: '安全中心汇总了所有上游的风险项，用它确认没有遗漏的宽松配置。',
  },
]
</script>

<template>
  <HelpArticle
    title="安全档位与安全中心"
    subtitle="安全档位只作用于 stdio 本地子进程。远程与 OpenAPI 上游不经过这条执行路径。"
    console-path="/security"
    console-label="安全中心"
  >
    <HelpSection
      title="三个档位"
      anchor="modes"
      description="档位可以全局设默认值，也可以在每个上游上单独覆盖。"
    >
      <HelpFields :items="modes" />
      <HelpCallout tone="info" title="策略不等于沙箱">
        没有 bubblewrap 时，文件与网络限制是策略层校验而非内核强制。运行环境页的「本地安全能力」
        会显示当前主机实际具备哪一种。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="严格档具体收紧了什么" anchor="strict">
      <HelpFields :items="strictItems" />
    </HelpSection>

    <HelpSection title="推荐的收紧顺序" anchor="rollout">
      <HelpSteps :steps="rollout" />
      <HelpCallout tone="success">
        不必所有上游都用严格档。对能接触敏感目录或需要出网的上游优先收紧，收益最高。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="安全中心看什么" anchor="center">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        安全中心把分散在各处的风险集中呈现：哪些上游用了完全放行、哪些声明了过宽的文件路径、
        哪些密钥没有限额。配置改动之后过来扫一眼，比逐个上游翻配置可靠。
      </p>
      <HelpCallout tone="warning" title="凭证不要交给不可信上游">
        stdio 上游在网关旁以同样的权限运行。父进程的敏感环境变量会被清理，但你在上游 env 里
        显式填写的凭证会照样注入 —— 只把凭证交给你信任的上游。
      </HelpCallout>
    </HelpSection>
  </HelpArticle>
</template>
