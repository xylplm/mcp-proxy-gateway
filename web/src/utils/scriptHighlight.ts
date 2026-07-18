/**
 * 轻量语法高亮（不引入 Monaco，避免首包膨胀）。
 * 预览足够用；编辑使用 textarea + 行号。
 */

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

export function highlightScript(content: string, language: string): string {
  const lang = (language || '').toLowerCase()
  const lines = content.split(/\r?\n/)
  return lines
    .map((line, idx) => {
      const n = String(idx + 1).padStart(3, ' ')
      return `<div class="script-line"><span class="script-ln">${n}</span><span class="script-code">${colorizeLine(line, lang)}</span></div>`
    })
    .join('')
}

function colorizeLine(line: string, lang: string): string {
  const trimmed = line.trimStart()
  if (
    lang === 'python' &&
    (trimmed.startsWith('#') || trimmed.startsWith('"""') || trimmed.startsWith("'''"))
  ) {
    return `<span class="tok-comment">${escapeHtml(line)}</span>`
  }
  if (
    (lang === 'javascript' || lang === 'js') &&
    (trimmed.startsWith('//') || trimmed.startsWith('/*'))
  ) {
    return `<span class="tok-comment">${escapeHtml(line)}</span>`
  }

  // 先分词、最后 escape，避免在已插入 span 标签的 HTML 上继续正则替换造成标签污染。
  const tokenPattern = /(['"`])(?:\\.|(?!\1).)*\1|\b\d+(?:\.\d+)?\b|\b[A-Za-z_][A-Za-z0-9_]*\b/g
  const keywords = new Set(
    lang === 'python'
      ? [
          'def',
          'class',
          'import',
          'from',
          'as',
          'return',
          'if',
          'elif',
          'else',
          'for',
          'while',
          'try',
          'except',
          'with',
          'yield',
          'async',
          'await',
          'True',
          'False',
          'None',
          'print',
        ]
      : [
          'const',
          'let',
          'var',
          'function',
          'return',
          'if',
          'else',
          'for',
          'while',
          'try',
          'catch',
          'async',
          'await',
          'import',
          'from',
          'export',
          'class',
          'new',
          'true',
          'false',
          'null',
          'undefined',
        ],
  )
  let out = ''
  let last = 0
  for (const match of line.matchAll(tokenPattern)) {
    const index = match.index ?? 0
    out += escapeHtml(line.slice(last, index))
    const token = match[0]
    if (/^['"`]/.test(token)) out += `<span class="tok-str">${escapeHtml(token)}</span>`
    else if (/^\d/.test(token)) out += `<span class="tok-num">${escapeHtml(token)}</span>`
    else if (keywords.has(token)) out += `<span class="tok-kw">${escapeHtml(token)}</span>`
    else out += escapeHtml(token)
    last = index + token.length
  }
  out += escapeHtml(line.slice(last))
  return out
}
