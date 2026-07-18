import assert from 'node:assert/strict'
import test from 'node:test'
import {
  exportScriptPackage,
  languageLabel,
  parseScriptImport,
  riskBadgeClass,
  riskLabel,
} from '../src/utils/scripts.ts'

test('script labels and risk tones', () => {
  assert.equal(languageLabel('python'), 'Python')
  assert.equal(languageLabel('javascript'), 'JavaScript')
  assert.equal(riskLabel('critical'), '极高')
  assert.match(riskBadgeClass('high'), /warning/)
})

test('raw script import infers language', () => {
  const py = parseScriptImport('demo.py', 'print(1)')
  assert.equal(py.language, 'python')
  assert.equal(py.runtime, 'python3')
  const js = parseScriptImport('demo.mjs', 'console.log(1)')
  assert.equal(js.language, 'javascript')
  assert.equal(js.runtime, 'node')
})

test('managed script export/import roundtrip', () => {
  const pkg = exportScriptPackage({
    id: 'scr_1',
    name: 'demo',
    description: 'd',
    language: 'python',
    runtime: 'python3',
    entryFile: 'main.py',
    tags: ['x'],
    status: 'active',
    currentVersion: 'v1',
    contentSha256: 'abc',
    risk: { level: 'low', score: 0, findings: [] },
    sizeBytes: 8,
    createdAt: '',
    updatedAt: '',
    content: 'print(1)',
  })
  const parsed = parseScriptImport('demo.mpg-script.json', pkg)
  assert.equal(parsed.name, 'demo')
  assert.equal(parsed.content, 'print(1)')
  assert.deepEqual(parsed.tags, ['x'])
})

test('invalid import rejected', () => {
  assert.throws(() => parseScriptImport('x.txt', 'x'))
  assert.throws(() => parseScriptImport('x.json', '{"format":"bad"}'))
})
