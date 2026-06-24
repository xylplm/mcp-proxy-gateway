export const MAX_UPSTREAM_IMPORT_FILE_BYTES = 1024 * 1024

export interface UpstreamImportFileValidation {
  ok: boolean
  error?: string
}

export function validateUpstreamImportFile(
  file: Pick<File, 'name' | 'size'>,
  maxBytes = MAX_UPSTREAM_IMPORT_FILE_BYTES,
): UpstreamImportFileValidation {
  if (file.size <= 0) {
    return { ok: false, error: '文件内容为空' }
  }
  if (file.size > maxBytes) {
    return { ok: false, error: `文件过大，最大支持 ${formatFileSize(maxBytes)}` }
  }
  const name = file.name.trim().toLowerCase()
  if (name !== '' && !name.endsWith('.json') && !name.endsWith('.txt')) {
    return { ok: false, error: '请选择 JSON 配置文件' }
  }
  return { ok: true }
}

export function formatFileSize(bytes: number): string {
  if (bytes >= 1024 * 1024) {
    return `${Math.round(bytes / 1024 / 1024)} MB`
  }
  if (bytes >= 1024) {
    return `${Math.round(bytes / 1024)} KB`
  }
  return `${Math.max(0, bytes)} B`
}
