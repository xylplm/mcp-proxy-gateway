import request from '@/api/request'

export interface BackupPreview {
  version: string
  upstreamCount: number
  aliasRuleCount: number
  mcpFilterRuleCount: number
  toolPolicyRuleCount: number
  apiKeyCount: number
  apiKeyFilterRuleCount: number
  aclCount: number
  containsSecrets: boolean
}

export interface BackupImportResult {
  imported: boolean
  restartRequested: boolean
  preview: BackupPreview
}

export async function exportBackup(): Promise<Blob> {
  const res = await request.get<Blob>('/backup/export', { responseType: 'blob' })
  return res.data
}

export async function previewBackup(content: string): Promise<BackupPreview> {
  const res = await request.post<BackupPreview>('/backup/preview', { content })
  return res.data
}

export async function importBackup(content: string): Promise<BackupImportResult> {
  const res = await request.post<BackupImportResult>('/backup/import', { content })
  return res.data
}
