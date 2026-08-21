<script setup lang="ts">
import HelpArticle from '@/components/help/HelpArticle.vue'
import HelpCallout from '@/components/help/HelpCallout.vue'
import HelpFields, { type HelpField } from '@/components/help/HelpFields.vue'
import HelpSection from '@/components/help/HelpSection.vue'
import HelpSteps, { type HelpStep } from '@/components/help/HelpSteps.vue'

const flavors: HelpField[] = [
  {
    name: ':latest / :full',
    tag: '默认',
    desc: '完整镜像，内置 Node 24、Python 3.12、uv 与 uvx，还有用于严格档隔离的 bubblewrap。stdio 上游、npm/pip 依赖管理、脚本中心都可用。',
  },
  {
    name: ':slim',
    desc: '精简镜像，只有网关本体，体积最小。只支持远程与 OpenAPI 上游；脚本中心、依赖管理会自动隐藏，模板市场也不再列出 stdio 服务。',
  },
]

const layout: HelpField[] = [
  {
    name: 'runtime/bin',
    desc: '你手动放可执行文件的地方，PATH 优先级最高。用于覆盖镜像自带版本，或补充镜像里没有的工具。放进去后需要有可执行权限。',
  },
  {
    name: 'runtime/npm',
    desc: 'npm 共享依赖。装在这里的 CLI 可以直接作为 stdio 启动命令，所有上游共享一份。',
  },
  {
    name: 'runtime/pip',
    desc: 'pip 共享依赖，平铺存放并自动接入子进程的模块搜索路径。装完即可被 Python 上游和脚本 import。',
  },
  {
    name: 'runtime/cache',
    desc: 'npm 与 uv 的下载缓存。放在数据卷里，重建容器不用重新下载。',
  },
]

const depSteps: HelpStep[] = [
  {
    title: '选 npm 还是 pip',
    desc: '取决于要装的包属于哪个生态。两边的语法不同：npm 用 名称@版本，pip 用 名称==版本。',
  },
  {
    title: '填包名并安装',
    desc: '支持一次填多个，用逗号分隔。安装过程有进度与日志，失败原因会完整保留下来。',
    hint: '只接受包名和版本号，不接受路径、URL 或命令行参数。',
  },
  {
    title: '在上游或脚本里使用',
    desc: '装好即可用，不需要额外配置环境变量。所有 stdio 上游与脚本共享同一份依赖。',
  },
]
</script>

<template>
  <HelpArticle
    title="运行环境与依赖"
    subtitle="只有 stdio 上游和脚本中心需要本地运行时。远程与 OpenAPI 上游完全不依赖本页的任何东西。"
    console-path="/runtime"
    console-label="运行环境"
  >
    <HelpSection
      title="两种镜像的差别"
      anchor="flavors"
      description="运行时由镜像固定提供，不在运行期下载安装。版本由镜像标签决定，可复现。"
    >
      <HelpFields :items="flavors" />
      <HelpCallout tone="info" title="怎么知道自己在用哪个">
        运行环境页标题旁有「完整镜像 / 精简镜像」徽标。精简镜像下会额外显示一段说明，
        告诉你哪些功能不可用以及如何切换。
      </HelpCallout>
    </HelpSection>

    <HelpSection
      title="数据卷里放什么"
      anchor="layout"
      description="解释器在镜像里，数据卷只保存依赖和你自己放的文件，因此容器更新不会丢依赖。"
    >
      <HelpFields :items="layout" />
    </HelpSection>

    <HelpSection title="安装共享依赖" anchor="deps">
      <HelpSteps :steps="depSteps" />
      <HelpCallout tone="warning" title="ESM 项目要注意">
        npm 共享依赖对 CLI 和 CommonJS 查找有效。如果你的上游是有自己 package.json 的 ESM 项目，
        依赖仍应装在那个项目目录里。
      </HelpCallout>
    </HelpSection>

    <HelpSection
      title="工具显示缺失怎么办"
      anchor="missing"
      description="官方完整镜像下正常不会缺。缺失一般出现在自建镜像或改过容器 PATH 的部署里。"
    >
      <HelpFields
        :items="[
          {
            name: '换用官方完整镜像',
            desc: '最省事的做法，自带全部解释器，数据卷可以直接复用，不需要迁移依赖。',
          },
          {
            name: '放入 runtime/bin',
            desc: '只缺个别工具时，把可执行文件放进去并加上可执行权限，该目录优先于镜像自带版本。',
          },
          {
            name: '刷新探测',
            desc: '放好文件后点「刷新探测」。若是改了容器入口的 PATH，需要重启容器才会生效。',
          },
        ]"
      />
    </HelpSection>

    <HelpSection title="从旧版本升级" anchor="upgrade">
      <p class="text-sm leading-7 text-gray-600 dark:text-gray-300">
        旧版会在数据卷里留下 node、python、uv、state
        四个目录。新版不再读写它们，容器启动时只会提示存在残留，
        不会自动删除。确认无用后手动删即可，不影响 npm 与 pip 依赖。
      </p>
      <HelpCallout tone="warning">
        旧版装在 Python 虚拟环境里的 pip 依赖不会自动迁移。需要的包在依赖管理的 pip 页重装一次即可。
      </HelpCallout>
    </HelpSection>
  </HelpArticle>
</template>
