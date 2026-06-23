import request from '@/api/request'

export async function exportDiagnostics(): Promise<Blob> {
  const res = await request.get<Blob>('/diagnostics/export', { responseType: 'blob' })
  return res.data
}
