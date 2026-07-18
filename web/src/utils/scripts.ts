import type { ScriptDetail, ScriptLanguage, ScriptRiskLevel } from '@/api/scripts'

export function exportScriptPackage(detail: ScriptDetail): string {
  return JSON.stringify(
    {
      format: 'mpg-script-v1',
      name: detail.name,
      description: detail.description ?? '',
      language: detail.language,
      runtime: detail.runtime,
      tags: detail.tags ?? [],
      entryFile: detail.entryFile,
      version: detail.currentVersion,
      contentSha256: detail.contentSha256 ?? '',
      risk: detail.risk,
      content: detail.content,
    },
    null,
    2,
  )
}

export function parseScriptImport(
  fileName: string,
  content: string,
): {
  name: string
  description: string
  language: 'python' | 'javascript'
  runtime: string
  tags: string[]
  content: string
  note: string
} {
  if (fileName.toLowerCase().endsWith('.json')) {
    const parsed = JSON.parse(content) as Record<string, unknown>
    if (parsed.format !== 'mpg-script-v1' || typeof parsed.content !== 'string') {
      throw new Error('脚本包格式非法')
    }
    const language = parsed.language === 'javascript' ? 'javascript' : 'python'
    return {
      name: typeof parsed.name === 'string' ? parsed.name : 'imported-script',
      description: typeof parsed.description === 'string' ? parsed.description : '',
      language,
      runtime:
        typeof parsed.runtime === 'string'
          ? parsed.runtime
          : language === 'python'
            ? 'python3'
            : 'node',
      tags: Array.isArray(parsed.tags)
        ? parsed.tags.filter((item): item is string => typeof item === 'string')
        : ['导入'],
      content: parsed.content,
      note: `导入脚本包 ${fileName}`,
    }
  }
  const ext = fileName.toLowerCase().split('.').pop() ?? ''
  if (!['py', 'js', 'mjs', 'cjs'].includes(ext)) {
    throw new Error('仅支持 .py、.js、.mjs、.cjs 或 MPG 脚本包 .json')
  }
  const language: 'python' | 'javascript' = ext === 'py' ? 'python' : 'javascript'
  return {
    name: fileName.replace(/\.[^.]+$/, ''),
    description: `从 ${fileName} 导入`,
    language,
    runtime: language === 'python' ? 'python3' : 'node',
    tags: ['导入'],
    content,
    note: `导入 ${fileName}`,
  }
}

export function riskBadgeClass(level: ScriptRiskLevel): string {
  switch (level) {
    case 'critical':
      return 'bg-error-50 text-error-700 ring-1 ring-error-200 dark:bg-error-500/10 dark:text-error-300'
    case 'high':
      return 'bg-warning-50 text-warning-800 ring-1 ring-warning-200 dark:bg-warning-500/10 dark:text-warning-300'
    case 'medium':
      return 'bg-brand-50 text-brand-700 ring-1 ring-brand-100 dark:bg-brand-500/10 dark:text-brand-300'
    default:
      return 'bg-success-50 text-success-700 ring-1 ring-success-200 dark:bg-success-500/10 dark:text-success-300'
  }
}

export function riskLabel(level: ScriptRiskLevel): string {
  switch (level) {
    case 'critical':
      return '极高'
    case 'high':
      return '高'
    case 'medium':
      return '中'
    default:
      return '低'
  }
}

export function languageLabel(lang: ScriptLanguage): string {
  if (lang === 'javascript') return 'JavaScript'
  if (lang === 'python') return 'Python'
  return String(lang || '未知')
}

export const SCRIPT_TEMPLATES: Record<
  'python' | 'javascript',
  { name: string; content: string; runtime: string }
> = {
  python: {
    name: 'python-hello-mcp',
    runtime: 'python3',
    content: `#!/usr/bin/env python3
"""最小可运行的 stdio MCP 占位脚本。

请替换为真实 MCP Server 实现；网关会以白名单解释器启动本文件。
"""

def main() -> None:
    print("hello from managed script", flush=True)


if __name__ == "__main__":
    main()
`,
  },
  javascript: {
    name: 'node-hello-mcp',
    runtime: 'node',
    content: `#!/usr/bin/env node
// 最小可运行的 stdio MCP 占位脚本。
// 请替换为真实 MCP Server 实现；网关会以白名单解释器启动本文件。

console.log('hello from managed script')
`,
  },
}
