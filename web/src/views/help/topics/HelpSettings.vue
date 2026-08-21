<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const connection: HelpField[] = [
  {
    name: '连接超时',
    desc: '建立上游连接的等待上限。远程上游偶发超时可以适当调大；调得过大会让故障上游拖慢整体响应。',
  },
  {
    name: '重试退避',
    desc: '连接失败后的重试节奏。上游不稳定时退避会逐步拉长间隔，避免把对方打得更惨。',
  },
  {
    name: 'stdio 总开关',
    desc: '一键禁用所有本地子进程执行。只用远程上游的部署建议直接关掉，减少一整类风险。',
  },
  {
    name: 'stdio 命令白名单',
    desc: '允许作为启动命令的可执行文件名。shell 与包装器类命令不可放行，这是硬约束。',
  },
]

const mirrors: HelpField[] = [
  {
    name: 'npm registry',
    desc: '影响依赖管理的 npm 安装与 stdio 子进程拉包。国内网络下配镜像源能显著提速。',
  },
  {
    name: 'pip / uv 索引',
    desc: '同理作用于 Python 侧。两项都建议只填公共或无认证的镜像地址。',
  },
]

const backup: HelpStep[] = [
  {
    title: '导出备份',
    desc: '在系统设置里导出配置备份，含上游、规则、密钥元数据等。建议在每次大改动之前先导一份。',
    hint: '备份不含 API Key 明文，恢复后需要重新分发密钥。',
  },
  {
    title: '妥善保存',
    desc: '备份里含上游凭证一类敏感信息，按机密文件对待，不要随手放进代码仓库。',
  },
  {
    title: '需要时恢复',
    desc: '恢复会覆盖现有配置，属于不可逆操作。执行前确认当前状态已另存一份。',
  },
]
</script>

<template>
  <HelpArticle
    title="系统设置"
    subtitle="全局开关与默认值。这里的改动影响所有上游，调整前先想清影响面。"
    console-path="/settings"
    console-label="系统设置"
  >
    <HelpSection
      title="连接与执行"
      anchor="connection"
      description="这几项直接决定上游的连接行为与本地执行范围。"
    >
      <HelpFields :items="connection" />
      <HelpCallout tone="warning" title="放行命令前先确认必要性">
        命令白名单是防止任意可执行文件被拉起的第一道闸。加一项之前先确认确实需要，
        并优先考虑能不能改用远程上游。
      </HelpCallout>
    </HelpSection>

    <HelpSection
      title="包仓库镜像"
      anchor="mirrors"
      description="对依赖管理命令和 stdio 子进程同时生效。"
    >
      <HelpFields :items="mirrors" />
      <HelpCallout tone="danger" title="不要配私有带认证的源">
        网关会清理子进程继承的敏感环境变量，不提供私有 registry 的凭证管理。
        填带认证信息的地址等于把凭证暴露给所有子进程。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="默认安全档位" anchor="security">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        这里设的是新建 stdio 上游时的默认档位，已有上游各自的设置不会被改动。
        档位的具体差异见「安全档位与安全中心」。
      </p>
    </HelpSection>

    <HelpSection title="备份与恢复" anchor="backup">
      <HelpSteps :steps="backup" />
    </HelpSection>
  </HelpArticle>
</template>
