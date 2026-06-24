import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildPlaygroundSchemaFields,
  initialSchemaFormValues,
  schemaArgsToFormValues,
  schemaFormDefaultArgs,
  schemaFormValuesToArgs,
} from '../src/utils/jsonSchemaPlayground.ts'

const schema = {
  type: 'object',
  required: ['query', 'limit'],
  properties: {
    query: {
      type: 'string',
      title: '搜索词',
      description: '需要搜索的关键词',
    },
    limit: {
      type: 'integer',
      default: 5,
    },
    exact: {
      type: 'boolean',
      default: false,
    },
    mode: {
      type: 'string',
      enum: ['fast', 'deep'],
    },
    filters: {
      type: 'object',
      default: { site: 'docs' },
    },
  },
}

test('builds playground fields from common JSON schema properties', () => {
  const fields = buildPlaygroundSchemaFields(schema)

  assert.equal(fields.length, 5)
  assert.deepEqual(
    fields.map((field) => [field.name, field.label, field.kind, field.required]),
    [
      ['query', '搜索词', 'string', true],
      ['limit', 'limit', 'integer', true],
      ['exact', 'exact', 'boolean', false],
      ['mode', 'mode', 'string', false],
      ['filters', 'filters', 'json', false],
    ],
  )
  assert.deepEqual(fields[3].enumOptions.map((option) => option.label), ['fast', 'deep'])
})

test('creates initial form values and default args from schema defaults', () => {
  const fields = buildPlaygroundSchemaFields(schema)

  assert.deepEqual(initialSchemaFormValues(fields), {
    query: '',
    limit: '5',
    exact: 'false',
    mode: '',
    filters: '{\n  "site": "docs"\n}',
  })
  assert.deepEqual(schemaFormDefaultArgs(fields), {
    limit: 5,
    exact: false,
    filters: { site: 'docs' },
  })
})

test('normalizes schema form values into playground arguments', () => {
  const fields = buildPlaygroundSchemaFields(schema)

  const got = schemaFormValuesToArgs(fields, {
    query: 'mcp gateway',
    limit: '10',
    exact: 'true',
    mode: '1',
    filters: '{"type":"guide"}',
  })

  assert.equal(got.ok, true)
  assert.deepEqual(got.value, {
    query: 'mcp gateway',
    limit: 10,
    exact: true,
    mode: 'deep',
    filters: { type: 'guide' },
  })
})

test('validates required and typed schema form values', () => {
  const fields = buildPlaygroundSchemaFields(schema)

  assert.deepEqual(schemaFormValuesToArgs(fields, { query: '', limit: '1' }), {
    ok: false,
    error: '搜索词 为必填项',
  })
  assert.deepEqual(schemaFormValuesToArgs(fields, { query: 'x', limit: '1.2' }), {
    ok: false,
    error: 'limit 必须是整数',
  })
  assert.deepEqual(schemaFormValuesToArgs(fields, { query: 'x', limit: '1', filters: '{' }), {
    ok: false,
    error: 'filters 不是合法 JSON',
  })
})

test('maps existing JSON args back into schema form values', () => {
  const fields = buildPlaygroundSchemaFields(schema)

  assert.deepEqual(schemaArgsToFormValues(fields, {
    query: 'status',
    limit: 3,
    exact: true,
    mode: 'fast',
    filters: { status: 'ok' },
  }), {
    query: 'status',
    limit: '3',
    exact: 'true',
    mode: '0',
    filters: '{\n  "status": "ok"\n}',
  })
})
