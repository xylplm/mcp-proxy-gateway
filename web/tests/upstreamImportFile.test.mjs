import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatFileSize,
  validateUpstreamImportFile,
} from '../src/utils/upstreamImportFile.ts'

test('validates import file size and extension', () => {
  assert.deepEqual(validateUpstreamImportFile({ name: 'mcp.json', size: 128 }), { ok: true })
  assert.deepEqual(validateUpstreamImportFile({ name: 'mcp.txt', size: 128 }), { ok: true })
  assert.equal(validateUpstreamImportFile({ name: 'mcp.yaml', size: 128 }).ok, false)
  assert.equal(validateUpstreamImportFile({ name: 'empty.json', size: 0 }).error, '文件内容为空')
  assert.equal(
    validateUpstreamImportFile({ name: 'big.json', size: 2048 }, 1024).error,
    '文件过大，最大支持 1 KB',
  )
})

test('formats file size for import hints', () => {
  assert.equal(formatFileSize(0), '0 B')
  assert.equal(formatFileSize(512), '512 B')
  assert.equal(formatFileSize(2048), '2 KB')
  assert.equal(formatFileSize(1024 * 1024), '1 MB')
})
