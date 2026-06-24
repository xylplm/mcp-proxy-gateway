import type { TransportType, UpstreamTestResult } from '@/api/upstreams'

export interface UpstreamTestDiagnostic {
  title: string
  description: string
  actions: string[]
}

export function testStageLabel(stage: string): string {
  switch (stage) {
    case 'connect':
      return '连接阶段'
    case 'list_tools':
      return '工具列表'
    case 'ok':
      return '测试完成'
    default:
      return stage
  }
}

export function upstreamTestDiagnostic(
  result: UpstreamTestResult | null,
  transport: TransportType,
): UpstreamTestDiagnostic | null {
  if (result === null || result.ok) return null
  const message = (result.message ?? '').toLowerCase()
  if (message.includes('timeout') || message.includes('deadline') || message.includes('timed out')) {
    return {
      title: '连接超时',
      description: '网关已发起连接，但上游没有在超时时间内完成响应。',
      actions:
        transport === 'stdio'
          ? [
              '确认命令能在当前运行环境中启动',
              '检查依赖是否已安装，例如 npx、uvx、python 或 docker',
              '确认命令启动后不会等待额外交互输入',
            ]
          : [
              '确认地址可以从网关所在机器访问',
              '检查代理、防火墙或容器网络映射',
              '确认上游服务路径和协议与所选接入方式一致',
            ],
    }
  }
  if (
    message.includes('unauthorized') ||
    message.includes('forbidden') ||
    message.includes('401') ||
    message.includes('403')
  ) {
    return {
      title: '认证未通过',
      description: '上游拒绝了当前凭证或请求头。',
      actions: [
        '确认 Token 或 API Key 没有过期',
        '检查认证方式和请求头名称是否与上游文档一致',
        '如果模板使用 ${credential}，确认凭证已填入认证区',
      ],
    }
  }
  if (message.includes('not found') || message.includes('404')) {
    return {
      title: '服务路径不可用',
      description: '网关连到了目标地址，但上游没有提供当前路径。',
      actions: ['确认 URL 是否以正确 MCP 路径结尾', '检查是否选错 SSE、Streamable HTTP 或 WebSocket', '确认反向代理没有改写或截断路径'],
    }
  }
  if (result.stage === 'list_tools') {
    return {
      title: '工具列表拉取失败',
      description: '连接已建立，但上游没有成功返回工具列表。',
      actions: ['确认上游实现支持 tools/list', '检查上游启动日志中的 schema 或权限错误', '尝试在上游本身的调试工具中列出工具'],
    }
  }
  if (transport === 'stdio') {
    return {
      title: '本地命令启动失败',
      description: '网关无法通过 stdio 启动或握手上游进程。',
      actions: ['检查命令、参数和工作目录是否正确', '确认网关运行用户有执行权限', '在网关所在环境中手动执行该命令验证输出'],
    }
  }
  return {
    title: '连接未建立',
    description: '网关还没有完成与上游的 MCP 握手。',
    actions: ['确认地址、协议和端口可达', '检查上游服务是否已启动', '查看系统日志获取更详细的连接错误'],
  }
}
