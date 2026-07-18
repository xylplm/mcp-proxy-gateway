/**
 * 网关主机路径浏览 API（管理员只读）。
 * 仅可浏览 data / runtime / global_file_roots / 表单上下文路径。
 */
import request from '@/api/request'

export type BrowseMode = 'directory' | 'file' | 'any'

export interface BrowseRoot {
  id: string
  label: string
  path: string
  kind: 'data' | 'runtime' | 'global_file' | 'context' | string
}

export interface BrowseEntry {
  name: string
  path: string
  type: 'dir' | 'file' | string
  size?: number
  modTime?: string
  readable: boolean
  enterable: boolean
}

export interface BrowseRootsResult {
  roots: BrowseRoot[]
  platform: string
  pathSeparator: string
  hostHint?: string
}

export interface BrowseListResult {
  path: string
  parent?: string
  entries: BrowseEntry[]
  truncated: boolean
  platform: string
  pathSeparator: string
}

export interface BrowseStatResult {
  path: string
  exists: boolean
  type: 'dir' | 'file' | 'missing' | 'other' | string
  allowed: boolean
  readable: boolean
}

function contextQuery(contextRoots?: string[]): string {
  if (!contextRoots || contextRoots.length === 0) return ''
  return contextRoots
    .map((item) => item.trim())
    .filter((item) => item !== '')
    .join(',')
}

export async function getBrowseRoots(contextRoots?: string[]): Promise<BrowseRootsResult> {
  const res = await request.get<BrowseRootsResult>('/fs/roots', {
    params: { context: contextQuery(contextRoots) || undefined },
  })
  return res.data
}

export async function listBrowseDir(options: {
  path: string
  mode?: BrowseMode
  limit?: number
  contextRoots?: string[]
  signal?: AbortSignal
}): Promise<BrowseListResult> {
  const res = await request.get<BrowseListResult>('/fs/list', {
    params: {
      path: options.path,
      mode: options.mode ?? 'directory',
      limit: options.limit,
      context: contextQuery(options.contextRoots) || undefined,
    },
    signal: options.signal,
  })
  return res.data
}

export async function statBrowsePath(options: {
  path: string
  contextRoots?: string[]
  signal?: AbortSignal
}): Promise<BrowseStatResult> {
  const res = await request.get<BrowseStatResult>('/fs/stat', {
    params: {
      path: options.path,
      context: contextQuery(options.contextRoots) || undefined,
    },
    signal: options.signal,
  })
  return res.data
}
