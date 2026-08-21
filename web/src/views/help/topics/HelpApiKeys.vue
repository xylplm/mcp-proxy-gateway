<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const steps: HelpStep[] = [
  {
    title: '创建密钥',
    desc: '填一个能看出用途的名称，例如「本机开发」「生产客户端」。名称只用于你自己识别。',
  },
  {
    title: '当场复制明文',
    desc: '明文只在创建成功的那个弹窗里显示一次，关闭后无法再查看。复制后立刻存到你的密码管理器。',
    hint: '弄丢了不用纠结找回：删掉旧的重建一个更快。',
  },
  {
    title: '勾选可用工具',
    desc: '默认建议按最小可用范围勾选。不勾选任何工具的密钥可以通过认证，但拿不到工具列表。',
  },
  {
    title: '设置限额',
    desc: '按这个密钥的实际用量设速率与配额。个人调试给小额度，生产按压测结果给。',
  },
]

const limits: HelpField[] = [
  {
    name: '工具授权',
    desc: '这个密钥能看到并调用的工具子集。与工具目录的可见性是与的关系：全局隐藏的工具，任何密钥都拿不到。',
  },
  {
    name: '速率限制',
    desc: '单位时间内的请求上限，防止某个客户端把上游打满。超限返回 429，客户端一般会自行退避重试。',
  },
  {
    name: '配额',
    desc: '累计调用次数上限，用于成本控制。用完之后该密钥停止服务，需要手动调整额度才能恢复。',
  },
  {
    name: '启用状态',
    desc: '临时封停用停用而不是删除：停用可随时恢复，删除后引用它的客户端只能换新密钥。',
  },
]
</script>

<template>
  <HelpArticle
    title="API Key 与配额"
    subtitle="密钥同时承担三件事：认证客户端、决定它能用哪些工具、限制它能用多少。"
    console-path="/apikeys"
    console-label="API Key 管理"
  >
    <HelpSection title="创建一个密钥" anchor="create">
      <HelpSteps :steps="steps" />
      <HelpCallout tone="danger" title="明文只显示一次">
        网关只保存密钥的哈希，不保存明文，因此无法在任何页面重新展示。这是为了万一数据库泄露也不会
        直接泄露可用凭证。
      </HelpCallout>
    </HelpSection>

    <HelpSection
      title="四类设置分别管什么"
      anchor="settings"
      description="搞清这四项的边界，就能设计出清晰的多客户端授权方案。"
    >
      <HelpFields :items="limits" />
    </HelpSection>

    <HelpSection title="推荐的划分方式" anchor="strategy">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        一个密钥对应一个使用场景，而不是一个人。这样某个场景出问题时可以单独停用，不影响其他接入；
        调用记录也能按密钥区分流量来源，排查时不用猜是谁发的请求。
      </p>
      <HelpCallout tone="success">
        只读场景（问答、检索）和写操作场景（提交、部署）用两个密钥分开授权，能显著降低误操作的影响面。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="怀疑密钥泄露" anchor="rotate">
      <HelpSteps
        :steps="[
          {
            title: '立刻停用',
            desc: '停用是即时生效的，比先去改限额更快止损。',
          },
          {
            title: '查调用记录',
            desc: '按这个密钥筛选最近的调用，确认有没有异常来源或异常工具被调用。',
          },
          {
            title: '建新密钥并替换',
            desc: '新建一个密钥、授权同样的工具，更新客户端配置后再删除旧的。',
          },
        ]"
      />
    </HelpSection>
  </HelpArticle>
</template>
