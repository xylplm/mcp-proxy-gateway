import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildSecurityProfilePayload,
  normalizeSecurityMode,
  normalizeSecurityProfile,
  preflightReadyLabelEx,
  preflightToneEx,
  securityModeBadgeClass,
  securityModeLabel,
  securityRiskLabel,
} from '../src/utils/stdioSecurity.ts'

test('normalizeSecurityMode falls back safely', () => {
  assert.equal(normalizeSecurityMode('STRICT'), 'strict')
  assert.equal(normalizeSecurityMode('nope', 'unrestricted'), 'unrestricted')
  assert.equal(normalizeSecurityMode(null), 'standard')
})

test('normalizeSecurityProfile keeps empty mode for inherit', () => {
  const p = normalizeSecurityProfile({ mode: '', fileAccess: { mode: 'allowlist', paths: ['/a'] } })
  assert.equal(p.mode, '')
  assert.equal(p.fileAccess?.mode, 'allowlist')
  assert.deepEqual(p.fileAccess?.paths, ['/a'])
})

test('normalizeSecurityProfile rejects non-objects', () => {
  assert.equal(normalizeSecurityProfile(null).mode, '')
  assert.equal(normalizeSecurityProfile('strict').mode, '')
})

test('buildSecurityProfilePayload maps modes', () => {
  const strict = buildSecurityProfilePayload({
    mode: 'strict',
    filePathsText: '/data/ws\n',
    networkMode: 'deny',
    networkHostsText: '',
    allowSelfInstall: false,
    note: '',
  })
  assert.equal(strict.mode, 'strict')
  assert.equal(strict.fileAccess?.mode, 'allowlist')
  assert.deepEqual(strict.fileAccess?.paths, ['/data/ws'])
  assert.equal(strict.allowSelfInstall, false)

  const open = buildSecurityProfilePayload({
    mode: 'unrestricted',
    filePathsText: '',
    networkMode: 'unrestricted',
    networkHostsText: '',
    allowSelfInstall: null,
    note: 'lab',
  })
  assert.equal(open.mode, 'unrestricted')
  assert.equal(open.fileAccess?.mode, 'unrestricted')
  assert.equal(open.note, 'lab')

  const withPkgs = buildSecurityProfilePayload({
    mode: 'strict',
    filePathsText: '/data',
    networkMode: 'deny',
    networkHostsText: '',
    packageAllowlistText: '@my-org/*\ncustom-mcp',
    allowSelfInstall: false,
    note: '',
  })
  assert.deepEqual(withPkgs.packageAllowlist, ['@my-org/*', 'custom-mcp'])
})

test('labels and risk helpers', () => {
  assert.equal(securityModeLabel('strict'), '严格安全')
  assert.equal(securityRiskLabel('critical'), '极高风险')
  assert.match(securityModeBadgeClass('unrestricted'), /error/)
})

test('preflight helpers surface security failures', () => {
  assert.equal(
    preflightReadyLabelEx(false, true, true, false, '工作目录缺失'),
    '工作目录缺失',
  )
  assert.equal(preflightToneEx(true, true, true, false), 'error')
  assert.equal(preflightToneEx(true, true, true, true, 'critical'), 'error')
  assert.equal(preflightToneEx(true, true, true, true, 'low'), 'success')
})
