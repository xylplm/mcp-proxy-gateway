<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const parts: HelpField[] = [
  {
    name: '匹配范围',
    desc: '决定这条规则作用在哪些工具上：某个上游的全部工具，或按名称模式命中的一批工具。',
  },
  {
    name: '动作',
    desc: '命中后做什么：改名（加前缀 / 替换）、改描述、隐藏。一条规则可以只做一件事，组合靠多条规则叠加。',
  },
  {
    name: '优先级',
    desc: '多条规则命中同一个工具时，按优先级依次应用，后面的覆盖前面的同类动作。改名和隐藏互不干扰。',
  },
  {
    name: '启用开关',
    desc: '规则可以先建好不启用。调试期建议保持停用，用预览确认效果后再打开。',
  },
]

const steps: HelpStep[] = [
  {
    title: '想清楚要改的是一批还是一个',
    desc: '只改一两个工具，直接去工具目录改别名更直观。规则是为「一批」而生的：整个上游加前缀、把所有实验性工具隐藏。',
  },
  {
    title: '建规则并填匹配条件',
    desc: '先选上游或写名称模式，再选动作。范围写得越窄越安全。',
  },
  {
    title: '一定要先预览',
    desc: '预览会列出这条规则实际命中的工具和改写后的结果。看到的就是客户端将会看到的。',
    hint: '预览是只读的，不会改动任何数据，可以放心多试几次。',
  },
  {
    title: '启用并回工具目录复核',
    desc: '启用后到工具目录看最终效果。多条规则叠加时，最终结果由优先级决定，目录页显示的才是真实生效值。',
  },
]
</script>

<template>
  <HelpArticle
    title="规则引擎"
    subtitle="批量改写工具的名称、描述与可见性。适合「整个上游统一处理」这类诉求，避免逐条手改。"
    console-path="/rules"
    console-label="规则管理"
  >
    <HelpSection
      title="一条规则由四部分组成"
      anchor="anatomy"
      description="理解这四块，就能预测任意规则组合的结果。"
    >
      <HelpFields :items="parts" />
    </HelpSection>

    <HelpSection title="建一条规则" anchor="create">
      <HelpSteps :steps="steps" />
      <HelpCallout tone="warning" title="先预览再启用">
        规则一启用就影响所有客户端看到的工具列表。名称模式写宽了可能把不相关的工具一起改掉，
        预览是成本最低的验证方式。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="规则和工具目录的关系" anchor="precedence">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        两者改的是同一层呈现。工具目录里的手动别名是针对单个工具的显式设置，规则是批量的。
        当两者对同一个工具都有主张时，工具目录页显示的结果就是最终生效的结果 —— 有疑问就以那里为准。
      </p>
      <HelpCallout tone="info">
        规则不改变上游的真实工具名。调用时网关会把改写后的名字映射回原名，上游侧无感。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="常见用法" anchor="recipes">
      <HelpFields
        :items="[
          {
            name: '给上游加前缀',
            desc: '两个上游功能重叠时，各自加前缀（如 github_ / gitlab_），一次性解决所有同名冲突。',
          },
          {
            name: '隐藏一整类工具',
            desc: '上游带了写操作而你只想开放读，用名称模式把 create / delete / update 一类批量隐藏。',
          },
          {
            name: '统一描述口径',
            desc: '上游描述是英文或过于技术，用规则统一改成团队习惯的中文表述，提升模型选择准确率。',
          },
        ]"
      />
    </HelpSection>
  </HelpArticle>
</template>
