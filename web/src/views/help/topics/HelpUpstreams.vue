<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const transports: HelpField[] = [
  {
    name: 'SSE / Streamable HTTP',
    tag: '最省事',
    desc: '远程服务，只需要一个 URL 和可选的鉴权头。不占本地资源，也不需要镜像里有 Node 或 Python。能选远程就选远程。',
  },
  {
    name: 'WebSocket',
    desc: '同样是远程服务，适合上游只提供 ws:// 端点的情况。',
  },
  {
    name: 'stdio',
    tag: '需运行时',
    desc: '在网关旁边启动一个本地子进程通信，例如 npx 或 uvx 拉起的官方 MCP 服务。需要完整镜像提供 Node / Python。',
  },
  {
    name: 'OpenAPI',
    desc: '把一份 OpenAPI 描述转成工具，用于本身不是 MCP 的普通 HTTP 接口。',
  },
]

const createSteps: HelpStep[] = [
  {
    title: '先试模板市场',
    desc: '新建上游时打开模板市场，挑一个服务会自动预填传输类型、启动命令与需要的参数，只剩凭证要你填。',
    hint: '精简镜像下模板市场会自动隐藏 stdio 形态的服务，列出来的都是当前镜像能跑的。',
  },
  {
    title: '填写连接参数',
    desc: '远程类型填地址与鉴权头；stdio 填启动命令与参数。表单会实时做依赖预检，缺什么会直接提示。',
    hint: '带 ${} 的占位符是模板留给你的空，保存前必须替换成真实值。',
  },
  {
    title: '测试连接',
    desc: '保存前点「测试连接」，网关会真的建一次连接并读取工具列表。通过了再保存，能省掉大部分反复排查。',
  },
  {
    title: '保存并查看状态',
    desc: '保存后列表里会显示连接状态。首次连接失败会按退避策略自动重试，不需要手动反复点。',
  },
]

const troubleshoot: HelpField[] = [
  {
    name: 'initialize: EOF',
    desc: '子进程启动后立刻退出。多半是命令或包名不存在、缺少必需的环境变量，或该包在当前镜像里跑不起来。展开上游详情看最近错误原文。',
  },
  {
    name: '未找到可执行文件',
    desc: '镜像里没有这个命令。完整镜像自带 node / npx / python / uv / uvx；其他工具需要自己放进运行时卷的 bin 目录。',
  },
  {
    name: '启动命令不被策略允许',
    desc: '命令不在系统设置的 stdio 命令白名单里。确认确实需要后再放行，白名单是防止任意命令被拉起的第一道闸。',
  },
  {
    name: '连接超时',
    desc: '远程地址不可达或响应太慢。先确认网关容器能访问该地址，再考虑在系统设置里调大连接超时。',
  },
]
</script>

<template>
  <HelpArticle
    title="接入上游 MCP"
    subtitle="上游是网关的数据来源。接进来之后，它的工具才会出现在工具目录里，才能被客户端调用。"
    console-path="/upstreams"
    console-label="上游 MCP 管理"
  >
    <HelpSection
      title="先选对传输类型"
      anchor="transport"
      description="这是最影响后续体验的一个选择。四种类型的能力相同，差别在于服务跑在哪里。"
    >
      <HelpFields :items="transports" />
      <HelpCallout tone="info" title="拿不定主意就按这个顺序">
        对方给了 URL → 选 SSE 或 Streamable HTTP；对方给的是一条
        <code class="font-mono">npx</code> / <code class="font-mono">uvx</code> 命令 → 选
        stdio；对方只有普通 REST 接口 → 选 OpenAPI。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="新建一个上游" anchor="create">
      <HelpSteps :steps="createSteps" />
    </HelpSection>

    <HelpSection
      title="连接不上时按这个顺序看"
      anchor="troubleshoot"
      description="上游详情里会显示最近一次错误原文，先读它再动配置，比盲试快得多。"
    >
      <HelpFields :items="troubleshoot" />
      <HelpCallout tone="warning" title="改完配置记得重连">
        修改连接参数后网关会重建会话。如果状态还停在旧的错误上，等一轮自动重试或手动触发一次测试连接。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="停用而不是删除" anchor="disable">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        临时不想用某个上游，把它停用即可：工具会从聚合结果里消失，配置和已授权的引用都保留。
        删除是不可逆的，且会让引用了它的规则与 API Key 授权失去目标。
      </p>
    </HelpSection>
  </HelpArticle>
</template>
