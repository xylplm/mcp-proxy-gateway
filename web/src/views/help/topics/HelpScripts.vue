<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const steps: HelpStep[] = [
  {
    title: '新建脚本',
    desc: '选语言（Python 或 JavaScript），起个名字，把代码写进编辑器。保存时会自动做语法检测。',
    hint: '语法检测在解释器不可用时会自动跳过，不会挡住保存。',
  },
  {
    title: '保存生成版本',
    desc: '每次保存产生一个新版本并记录内容摘要。历史版本一直保留，可以随时对比与回滚。',
  },
  {
    title: '绑定为上游',
    desc: '在上游 MCP 管理里新建 stdio 上游，选择「来自脚本中心」并指定脚本与版本，网关会用对应解释器拉起它。',
  },
  {
    title: '像普通上游一样使用',
    desc: '连上之后它的工具会出现在工具目录里，同样受规则、可见性与 API Key 授权约束。',
  },
]

const versions: HelpField[] = [
  {
    name: '版本号',
    desc: '每次保存自增。上游绑定的是具体版本，因此改脚本不会立刻影响线上，需要显式把上游切到新版本。',
  },
  {
    name: '内容摘要',
    desc: '每个版本记录内容哈希，用于确认线上跑的到底是哪一份代码。',
  },
  {
    name: '回滚',
    desc: '发现新版本有问题，把上游切回上一个版本即可，旧代码始终在。',
  },
  {
    name: '风险提示',
    desc: '保存时会扫描代码里的高风险模式（执行外部命令、访问网络等）并给出提示，供你自己判断。',
  },
]
</script>

<template>
  <HelpArticle
    title="脚本中心"
    subtitle="把一段 Python 或 JavaScript 变成可被客户端调用的 MCP 上游，不需要单独部署一个服务。"
    console-path="/scripts"
    console-label="脚本中心"
  >
    <HelpCallout tone="warning" title="需要完整镜像">
      脚本靠 Node / Python 解释器执行。精简镜像里没有解释器，脚本可以编辑保存但无法运行，
      侧边栏也不会显示脚本中心入口。
    </HelpCallout>

    <HelpSection
      title="从代码到可调用工具"
      anchor="steps"
      description="脚本本身要实现 MCP 的 stdio 协议，网关负责把它拉起来并接管生命周期。"
    >
      <HelpSteps :steps="steps" />
    </HelpSection>

    <HelpSection title="版本管理" anchor="versions">
      <HelpFields :items="versions" />
      <HelpCallout tone="info">
        上游绑定固定版本这个设计是有意的：编辑脚本不会意外影响正在服务的连接，
        什么时候上线由你决定。
      </HelpCallout>
    </HelpSection>

    <HelpSection title="能用哪些第三方库" anchor="deps">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        脚本可以直接使用「运行环境 → 依赖管理」里装好的 npm 与 pip
        包，不需要在脚本里做任何路径处理。 需要新库时先去装，再回来写代码。
      </p>
    </HelpSection>

    <HelpSection title="脚本跑不起来时" anchor="troubleshoot">
      <HelpFields
        :items="[
          {
            name: '上游状态是失败，错误里有 EOF',
            desc: '脚本启动后立刻退出。多数是代码抛异常，或没有正确实现 stdio 协议的握手。',
          },
          {
            name: 'ModuleNotFoundError / Cannot find module',
            desc: '缺依赖。到运行环境的依赖管理里装上对应包，不要在脚本里自行安装。',
          },
          {
            name: '严格安全档位下启动失败',
            desc: '严格档会限制文件与网络访问。确认脚本需要的目录已加入文件允许路径，否则先用标准档验证功能。',
          },
        ]"
      />
      <HelpCallout tone="info" title="日志在哪">
        脚本的输出会体现在上游连接的错误详情与系统日志里。调试期建议先在标准档下跑通，再逐步收紧安全档位。
      </HelpCallout>
    </HelpSection>
  </HelpArticle>
</template>
