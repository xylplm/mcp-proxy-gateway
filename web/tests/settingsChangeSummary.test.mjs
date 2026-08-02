import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildSettingsDraft,
  cloneSettingsConfig,
  collectSettingsChanges,
  settingsChangesRequireRestart,
  settingsConfirmMessage,
} from '../src/utils/settingsChangeSummary.ts'

function baseConfig() {
  return {
    server: {
      admin_addr: ':8080',
      public_mcp_addr: '',
      expose_mcp_on_admin_addr: true,
      log_level: 'info',
    },
    admin: { username: '', password_hash: '', initialized: true },
    jwt_secret: '',
    auth: { session_timeout_s: 3600 },
    sync: { cron: '0 */30 * * * *', timeout_s: 30 },
    connection: {
      connect_timeout_s: 30,
      retry_initial_backoff_s: 5,
      retry_multiplier: 2,
      retry_max_backoff_s: 300,
      failure_threshold: 10,
      demand_reconnect_cooldown_s: 5,
      demand_reconnect_wait_s: 8,
    },
    aggregation: {
      upstream_call_timeout_s: 30,
      tool_routing_strategy: 'round_robin',
    },
    mcp_api: { smart_discovery_limit: 50 },
    statistics: { top_limit_default: 10, retention_days: 90 },
    audit: { page_size_default: 20, retention_days: 180 },
    security: {
      mode: 'monitor',
      failure_window_s: 300,
      max_failures_per_ip: 30,
      max_failures_per_key_fingerprint: 8,
      max_acl_denies_per_key_ip: 5,
      first_block_duration_s: 900,
      max_block_duration_s: 86400,
      escalation_window_s: 86400,
      trusted_proxy_cidrs: [],
      exempt_cidrs: [],
    },
    runtime: {
      stdio_enabled: true,
      command_allowlist: ['node', 'npx'],
      extra_sensitive_env_prefixes: [],
    },
    xiaozhi: { enabled: false, endpoint: '', mode: 'full' },
  }
}

test('collectSettingsChanges returns no changes for identical configs', () => {
  const before = baseConfig()
  assert.deepEqual(collectSettingsChanges(before, cloneSettingsConfig(before)), [])
})

test('runtime-only settings do not require restart', () => {
  const before = baseConfig()
  const after = cloneSettingsConfig(before)
  after.server.log_level = 'debug'
  after.mcp_api.smart_discovery_limit = 80

  const changes = collectSettingsChanges(before, after)
  assert.equal(changes.length, 2)
  assert.equal(settingsChangesRequireRestart(changes), false)
  assert.match(settingsConfirmMessage(changes), /2/)
})

test('listener settings require restart and appear in the confirmation message', () => {
  const before = baseConfig()
  const after = cloneSettingsConfig(before)
  after.server.public_mcp_addr = ':8081'

  const changes = collectSettingsChanges(before, after)
  assert.equal(changes.length, 1)
  assert.equal(changes[0].label, '独立 MCP 监听端口')
  assert.equal(settingsChangesRequireRestart(changes), true)
  assert.match(settingsConfirmMessage(changes), /8081/)
})

test('buildSettingsDraft normalizes form-only fields without mutating the source config', () => {
  const before = baseConfig()
  const draft = buildSettingsDraft(before, {
    adminAddr: ':9000',
    publicMCPAddr: '',
    trustedProxyCIDRs: ['10.0.0.0/8'],
    exemptCIDRs: ['192.168.0.0/16'],
    commandAllowlist: ['node', 'uvx'],
    globalFileRoots: ['/data/ws'],
    browseExtraRoots: ['/opt/mcp'],
    extraSensitiveEnvPrefixes: ['CORP_'],
  })

  assert.equal(draft.server.admin_addr, ':9000')
  assert.deepEqual(before.security.trusted_proxy_cidrs, [])
  assert.deepEqual(draft.security.trusted_proxy_cidrs, ['10.0.0.0/8'])
  assert.deepEqual(draft.runtime.command_allowlist, ['node', 'uvx'])
  assert.deepEqual(draft.runtime.global_file_roots, ['/data/ws'])
  assert.deepEqual(draft.runtime.browse_extra_roots, ['/opt/mcp'])
  assert.deepEqual(before.runtime.command_allowlist, ['node', 'npx'])
})

test('browse extra roots changes are runtime-only and labeled', () => {
  const before = baseConfig()
  const after = cloneSettingsConfig(before)
  after.runtime.browse_extra_roots = ['/opt/mcp-data']
  const changes = collectSettingsChanges(before, after)
  assert.equal(changes.length, 1)
  assert.equal(changes[0].label, '路径浏览额外根')
  assert.equal(settingsChangesRequireRestart(changes), false)
})
