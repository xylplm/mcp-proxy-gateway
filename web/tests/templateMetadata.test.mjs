import assert from 'node:assert/strict'
import test from 'node:test'
import {
  credentialTypeLabel,
  runtimeLabel,
  templateCardChips,
  templateMetadataSearchText,
  templateMetaChips,
  toolTypeLabel,
  trustLevelLabel,
} from '../src/utils/templateMetadata.ts'

const template = {
  id: 'github-mcp',
  name: 'GitHub',
  category: 'dev_tools',
  summary: '代码托管',
  docUrl: '',
  // github-mcp 已改为官方托管的远程 Streamable HTTP：容器内无法执行 docker run。
  transport: 'streamable-http',
  trustLevel: 'curated',
  runtimes: ['remote'],
  credentialTypes: ['token'],
  containerReady: true,
  toolTypes: ['dev_tools', 'project_management'],
  presetParams: {},
  placeholders: [],
}

test('formats template metadata labels', () => {
  assert.equal(trustLevelLabel('curated'), '内置精选')
  assert.equal(runtimeLabel('uvx'), 'uvx')
  assert.equal(credentialTypeLabel('api_key'), 'API Key')
  assert.equal(toolTypeLabel('project_management'), '项目管理')
})

test('builds compact and full template metadata chips', () => {
  const chips = templateMetaChips(template)
  assert.deepEqual(
    chips.map((chip) => chip.label),
    ['内置精选', 'Token', '远程', '容器友好', '开发工具', '项目管理'],
  )

  assert.deepEqual(
    templateCardChips(template).map((chip) => chip.label),
    ['Token', '远程', '开发工具'],
  )
})

test('exposes metadata as searchable text', () => {
  assert.match(templateMetadataSearchText(template), /远程/)
  assert.match(templateMetadataSearchText(template), /项目管理/)
})
