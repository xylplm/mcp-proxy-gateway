import assert from 'node:assert/strict'
import test from 'node:test'
import {
  filterTemplatesByPreference,
  markTemplateRecentlyUsed,
  normalizeTemplateMarketPrefs,
  toggleTemplateFavorite,
} from '../src/utils/templateMarketPrefs.ts'

const templates = [
  { id: 'github-mcp', name: 'GitHub' },
  { id: 'playwright-mcp', name: 'Playwright' },
  { id: 'tavily-search', name: 'Tavily' },
]

test('normalizes template market prefs defensively', () => {
  assert.deepEqual(
    normalizeTemplateMarketPrefs({
      favoriteIds: ['github-mcp', ' ', 'github-mcp', 12],
      recentIds: ['tavily-search', 'github-mcp', 'tavily-search'],
    }),
    {
      favoriteIds: ['github-mcp'],
      recentIds: ['tavily-search', 'github-mcp'],
    },
  )
  assert.deepEqual(normalizeTemplateMarketPrefs(null), { favoriteIds: [], recentIds: [] })
})

test('toggles favorite template ids without touching recent order', () => {
  const added = toggleTemplateFavorite({ favoriteIds: [], recentIds: ['github-mcp'] }, 'tavily-search')
  assert.deepEqual(added, { favoriteIds: ['tavily-search'], recentIds: ['github-mcp'] })

  const removed = toggleTemplateFavorite(added, 'tavily-search')
  assert.deepEqual(removed, { favoriteIds: [], recentIds: ['github-mcp'] })
})

test('marks recently used templates first and keeps unique bounded history', () => {
  const prefs = markTemplateRecentlyUsed(
    { favoriteIds: ['github-mcp'], recentIds: ['github-mcp', 'playwright-mcp'] },
    'tavily-search',
    2,
  )

  assert.deepEqual(prefs, {
    favoriteIds: ['github-mcp'],
    recentIds: ['tavily-search', 'github-mcp'],
  })
})

test('filters templates by favorites and recent preference order', () => {
  const prefs = {
    favoriteIds: ['playwright-mcp'],
    recentIds: ['tavily-search', 'github-mcp'],
  }

  assert.deepEqual(filterTemplatesByPreference(templates, prefs, 'all').map((tpl) => tpl.id), [
    'github-mcp',
    'playwright-mcp',
    'tavily-search',
  ])
  assert.deepEqual(filterTemplatesByPreference(templates, prefs, 'favorites').map((tpl) => tpl.id), [
    'playwright-mcp',
  ])
  assert.deepEqual(filterTemplatesByPreference(templates, prefs, 'recent').map((tpl) => tpl.id), [
    'tavily-search',
    'github-mcp',
  ])
})
