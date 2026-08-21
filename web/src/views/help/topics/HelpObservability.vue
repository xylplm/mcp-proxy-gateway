<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const pages: HelpField[] = [
  {
    name: '调用统计',
    desc: '按时间、上游、工具聚合的趋势与排行。用来回答「整体健康吗、谁在被大量调用、失败率有没有上升」。',
  },
  {
    name: '调用记录',
    desc: '逐条请求的明细，含入参、结果、耗时、所用密钥与错误原文。定位某一次具体失败就看这里。',
  },
  {
    name: '审计日志',
    desc: '管理台上的变更留痕：谁在什么时候改了哪个上游、密钥、规则。用于追溯配置变动。',
  },
  {
    name: '系统日志',
    desc: '网关进程自身的运行日志。上游连接建立失败、后台任务异常这类进程级问题在这里。',
  },
]

const flow: HelpStep[] = [
  {
    title: '先看调用记录里有没有这条请求',
    desc: '没有记录说明请求没到网关：查客户端配置、网络连通性、密钥是否填对。',
  },
  {
    title: '有记录就读错误原文',
    desc: '记录里保留了上游返回的原始错误。多数问题（参数不对、上游报错、超时）在这一步就能定性。',
  },
  {
    title: '错误指向连接层就查上游',
    desc: '出现 UPSTREAM_UNAVAILABLE 一类错误时，去上游详情看连接状态与最近错误，必要时再看系统日志。',
  },
  {
    title: '怀疑是配置被改动，查审计日志',
    desc: '「昨天还好好的」这类问题，先按时间范围翻审计日志，往往能直接找到那次改动。',
  },
]
</script>

<template>
  <HelpArticle
    title="统计、记录与日志"
    subtitle="四个页面分工不同。用对了能几分钟定位问题，用错了会在大量日志里白翻半天。"
    console-path="/statistics"
    console-label="调用统计"
  >
    <HelpSection
      title="四个页面各看什么"
      anchor="pages"
      description="按「从面到点」排列：统计看整体，记录看单次，审计看变更，系统日志看进程。"
    >
      <HelpFields :items="pages" />
    </HelpSection>

    <HelpSection title="标准排查顺序" anchor="flow">
      <HelpSteps :steps="flow" />
      <HelpCallout tone="info" title="先定位在哪一层">
        客户端、网关、上游三层。调用记录能一眼分清「没到网关」「网关拒了」「上游失败」，
        这个判断做对了，后面查什么就很明确。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="看统计时留意什么" anchor="metrics">
      <HelpFields
        :items="[
          {
            name: '失败率突然上升',
            desc: '通常是某个上游挂了或凭证过期。按上游维度看排行，能快速锁定是哪一个。',
          },
          {
            name: '某个工具调用量异常高',
            desc: '可能是模型在反复重试同一个工具，往往意味着这个工具的描述有歧义或它总是返回失败。',
          },
          {
            name: '耗时明显变长',
            desc: '先看是集中在某个上游还是全面变慢。全面变慢更可能是网关所在主机资源紧张。',
          },
        ]"
      />
    </HelpSection>

    <HelpSection title="记录保留与隐私" anchor="retention">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        调用记录会保存请求参数与返回结果，便于复盘，但也意味着敏感内容可能留痕。
        在放开给更多人访问管理台之前，先确认这一点是否可接受，并在系统设置里按需调整保留策略。
      </p>
    </HelpSection>
  </HelpArticle>
</template>
