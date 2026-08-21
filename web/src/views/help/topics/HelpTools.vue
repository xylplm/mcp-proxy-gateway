<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const controls: HelpField[] = [
  {
    name: '别名',
    desc: '改写暴露给客户端的工具名。上游原名不变，调用时网关自动映射回去。多个上游有同名工具时靠它区分。',
  },
  {
    name: '显示名 / 描述',
    desc: '改写客户端看到的说明文字。上游描述写得太技术或太笼统时，改成贴合你团队语境的说法能明显提升模型选对工具的概率。',
  },
  {
    name: '可见性',
    desc: '隐藏后该工具不出现在任何客户端的工具列表里，也不能被调用。适合上游带了一堆你不想暴露的能力。',
  },
  {
    name: '按 API Key 授权',
    desc: '可见性是全局开关，API Key 的工具授权是每个密钥单独的子集。两者是与的关系：全局隐藏的工具，任何密钥都拿不到。',
  },
]

const renameSteps: HelpStep[] = [
  {
    title: '定位工具',
    desc: '在工具目录里按上游或名称筛选，找到要调整的那一条。',
  },
  {
    title: '改别名或描述',
    desc: '编辑后保存即时生效，客户端下次拉取工具列表就能看到新名字。',
    hint: '别名要保持在客户端可接受的命名范围内：建议只用字母、数字、下划线和连字符。',
  },
  {
    title: '批量场景改用规则',
    desc: '如果是「所有来自某个上游的工具都加个前缀」这类批量诉求，用规则引擎更合适，不必逐条改。',
  },
]
</script>

<template>
  <HelpArticle
    title="工具目录与可见性"
    subtitle="所有上游的工具汇总在这里。客户端最终看到什么名字、能用哪些工具，都在这一层决定。"
    console-path="/tools"
    console-label="工具目录"
  >
    <HelpSection
      title="工具是怎么来的"
      anchor="source"
      description="网关连上上游后读取它的工具列表，缓存下来并合并成一份统一目录。"
    >
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        上游新增或删除工具后，需要网关重新拉取才会反映到目录里。目录页提供刷新，也会在上游重连时自动更新。
        如果某个上游的工具一直不出现，先回到上游列表确认它的连接状态。
      </p>
    </HelpSection>

    <HelpSection title="四种控制手段" anchor="controls">
      <HelpFields :items="controls" />
      <HelpCallout tone="warning" title="同名冲突要主动处理">
        两个上游都有
        <code class="font-mono">search</code> 时，客户端只会看到一个。给其中一个起别名，
        比让网关按不确定的顺序取舍要可靠。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="改一个工具的呈现" anchor="rename">
      <HelpSteps :steps="renameSteps" />
    </HelpSection>

    <HelpSection title="让模型少选错工具" anchor="quality">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        模型是靠名称和描述来判断该调哪个工具的。实践中最有效的两件事：把名字改成动词加对象的形式
        （<code class="font-mono">search_docs</code> 优于 <code class="font-mono">query</code>），
        以及在描述里写清「什么时候该用它」而不只是「它做什么」。
      </p>
      <HelpCallout tone="info">
        暴露的工具越少，模型选错的概率越低。把用不到的工具隐藏掉，通常比反复调描述更管用。
      </HelpCallout>
    </HelpSection>
  </HelpArticle>
</template>
