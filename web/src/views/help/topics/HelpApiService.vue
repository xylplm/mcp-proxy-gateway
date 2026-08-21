<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const steps: HelpStep[] = [
  {
    title: '准备一个 API Key',
    desc: '客户端靠密钥认证，并由密钥决定能看到哪些工具。没有密钥就无法接入。',
    hint: '还没有的话先去 API Key 管理创建，明文只在创建时显示一次。',
  },
  {
    title: '在 API 服务页取端点',
    desc: '页面会列出网关对外的端点地址，并提供可直接粘贴的客户端配置片段。',
  },
  {
    title: '粘贴到客户端配置',
    desc: '把片段填进客户端的 MCP 配置文件，重启客户端，工具列表里就会出现网关聚合后的工具。',
    hint: '客户端一般需要完全重启才会重新读取 MCP 配置。',
  },
  {
    title: '验证一次真实调用',
    desc: '在客户端里触发一个工具调用，然后到「调用记录」确认这条请求进来了、结果是否成功。',
  },
]

const troubleshoot: HelpField[] = [
  {
    name: '客户端看不到任何工具',
    desc: '依次确认：密钥是否有效、该密钥是否勾选了工具、这些工具在工具目录里是否被隐藏、上游是否处于已连接状态。',
  },
  {
    name: '401 / 认证失败',
    desc: '密钥填错或已被禁用、删除。密钥明文无法找回，确认不了就重建一个。',
  },
  {
    name: '429 / 请求过多',
    desc: '触发了该密钥的速率限制或配额上限。到 API Key 管理调整限额，或等窗口重置。',
  },
  {
    name: '工具能看到但调用报错',
    desc: '问题通常在上游而不是接入层。到「调用记录」看这次调用的错误详情，再到上游详情看连接状态。',
  },
]
</script>

<template>
  <HelpArticle
    title="客户端接入网关"
    subtitle="把网关配进 Claude、Cursor 等支持 MCP 的客户端，之后客户端只需要连网关一个地址。"
    console-path="/api-service"
    console-label="API 服务"
  >
    <HelpSection
      title="接入四步"
      anchor="steps"
      description="整个过程不需要改客户端代码，只改一份配置。"
    >
      <HelpSteps :steps="steps" />
    </HelpSection>

    <HelpSection title="聚合入口的好处" anchor="why">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        客户端只认识网关一个端点。之后增删上游、改工具名、调整授权，都在网关侧完成，
        客户端配置不用再动，也不必在每台机器上重复配一遍各个 MCP 服务的凭证。
      </p>
      <HelpCallout tone="success">
        给不同用途建不同的 API
        Key（例如开发一个、生产一个），就能用同一套上游对外提供不同的工具子集。
      </HelpCallout>
    </HelpSection>

    <HelpSection
      title="OpenAPI 直接调用"
      anchor="openapi"
      description="除了 MCP 协议，网关也把工具暴露为普通 HTTP 接口，便于脚本和自有服务调用。"
    >
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        API 服务页提供在线接口文档，可以直接查看每个工具的入参结构并试调。鉴权方式与 MCP 一致，
        用同一个 API Key，工具授权与配额也照样生效。
      </p>
    </HelpSection>

    <HelpSection title="接入不成功时" anchor="troubleshoot">
      <HelpFields :items="troubleshoot" />
      <HelpCallout tone="info" title="定位顺序">
        先看「调用记录」判断请求有没有到网关：没有记录说明问题在客户端配置或网络；
        有记录但失败，就按记录里的错误往上游查。
      </HelpCallout>
    </HelpSection>
  </HelpArticle>
</template>
