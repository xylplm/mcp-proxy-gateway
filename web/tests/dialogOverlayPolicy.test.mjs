/**
 * 弹窗遮罩策略守卫。
 *
 * 产品约定：模态框与侧滑抽屉只能通过显式的关闭/取消按钮关闭，不接受点击遮罩关闭。
 * 原因是遮罩面积大且紧贴表单，误触会直接丢弃未保存的编辑内容，代价与收益不成比例。
 *
 * 这里扫描所有 .vue，若在「遮罩元素」上重新出现 .self 关闭处理就失败，避免新增弹窗
 * 沿用旧写法把该行为悄悄带回来。判定范围限定在 fixed inset-0 附近，因此 .self 用于
 * 非遮罩场景不会误报。
 */
import assert from 'node:assert/strict'
import test from 'node:test'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC_DIR = fileURLToPath(new URL('../src', import.meta.url))

/** 遮罩标记与 .self 处理之间的最大字符距离（同一个开标签内足够宽裕）。 */
const OVERLAY_PROXIMITY = 400

const OVERLAY_MARKER = 'fixed inset-0'
const SELF_HANDLER = /@(?:click|mousedown|pointerdown|touchstart)\.self\b[^\n]*/g

function collectVueFiles(dir) {
  const found = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) found.push(...collectVueFiles(full))
    else if (entry.endsWith('.vue')) found.push(full)
  }
  return found
}

/** 返回落在遮罩元素上的 .self 处理列表（相对 src 的路径 + 命中片段）。 */
export function findOverlaySelfHandlers(source) {
  const hits = []
  for (const match of source.matchAll(SELF_HANDLER)) {
    const from = Math.max(0, match.index - OVERLAY_PROXIMITY)
    const nearby = source.slice(from, match.index)
    if (nearby.includes(OVERLAY_MARKER)) hits.push(match[0].trim())
  }
  return hits
}

test('模态框与抽屉的遮罩不得绑定关闭处理', () => {
  const offenders = []
  for (const file of collectVueFiles(SRC_DIR)) {
    const hits = findOverlaySelfHandlers(readFileSync(file, 'utf8'))
    if (hits.length > 0) {
      offenders.push(`${file.slice(SRC_DIR.length + 1).replace(/\\/g, '/')}: ${hits.join(' / ')}`)
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `遮罩不得关闭弹窗，请改由关闭/取消按钮触发；命中：\n${offenders.join('\n')}`,
  )
})

test('守卫能识别遮罩上的 .self 关闭处理', () => {
  const overlay = `<div v-if="open" class="fixed inset-0 z-[100001] p-4" @click.self="close" >`
  assert.deepEqual(findOverlaySelfHandlers(overlay), ['@click.self="close" >'])

  // 非遮罩元素上的 .self 不属于本策略范围，不应误报。
  const inner = `<div class="flex items-center gap-2" @click.self="toggle"></div>`
  assert.deepEqual(findOverlaySelfHandlers(inner), [])
})
