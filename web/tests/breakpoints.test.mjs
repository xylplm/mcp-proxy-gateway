import assert from 'node:assert/strict'
import test from 'node:test'
import {
  Breakpoint,
  CONTENT_MAX_WIDTH,
  PAGE_SIZE_BY_BREAKPOINT,
  SIDEBAR_MODE_BY_BREAKPOINT,
  SidebarMode,
  resolveBreakpoint,
  shouldUseSidebarDrawer,
} from '../src/constants/breakpoints.ts'

test('resolves the five viewport breakpoints', () => {
  assert.equal(resolveBreakpoint(390), Breakpoint.Mobile)
  assert.equal(resolveBreakpoint(768), Breakpoint.Tablet)
  assert.equal(resolveBreakpoint(1024), Breakpoint.Desktop)
  assert.equal(resolveBreakpoint(1440), Breakpoint.Wide)
  assert.equal(resolveBreakpoint(2560), Breakpoint.UltraWide)
})

test('keeps sidebar as a drawer on mobile and tablet only', () => {
  assert.equal(shouldUseSidebarDrawer(390), true)
  assert.equal(shouldUseSidebarDrawer(768), true)
  assert.equal(shouldUseSidebarDrawer(1023), true)
  assert.equal(shouldUseSidebarDrawer(1024), false)
  assert.equal(shouldUseSidebarDrawer(2560), false)
  assert.equal(SIDEBAR_MODE_BY_BREAKPOINT[Breakpoint.Mobile], SidebarMode.Drawer)
  assert.equal(SIDEBAR_MODE_BY_BREAKPOINT[Breakpoint.Tablet], SidebarMode.Drawer)
  assert.equal(SIDEBAR_MODE_BY_BREAKPOINT[Breakpoint.Desktop], SidebarMode.Expanded)
})

test('keeps large screen density and content width bounded', () => {
  assert.equal(PAGE_SIZE_BY_BREAKPOINT[Breakpoint.Mobile], 10)
  assert.equal(PAGE_SIZE_BY_BREAKPOINT[Breakpoint.Desktop], 20)
  assert.equal(PAGE_SIZE_BY_BREAKPOINT[Breakpoint.Wide], 50)
  assert.equal(CONTENT_MAX_WIDTH[Breakpoint.Wide], 1600)
  assert.equal(CONTENT_MAX_WIDTH[Breakpoint.UltraWide], 2048)
})
