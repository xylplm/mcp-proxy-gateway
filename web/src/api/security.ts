import request from '@/api/request'

export type SecurityEventType = 'auth_failed' | 'acl_denied' | 'blocked' | 'released' | string
export type SecuritySubjectType = 'ip' | 'key_fingerprint' | 'api_key_ip' | string
export type SecurityBlockStatus = 'active' | 'released' | 'expired' | string

export interface SecuritySummary {
  ActiveBlocks: number
  AuthFailures24h: number
  ACLDenies24h: number
  HighRiskSubjects24h: number
}

export interface SecurityEvent {
  ID: number
  EventType: SecurityEventType
  SubjectType: SecuritySubjectType
  Subject: string
  ClientIP: string
  APIKeyID: string
  APIKeyPrefix: string
  KeyFingerprint: string
  Method: string
  Path: string
  UserAgent: string
  Reason: string
  Count: number
  CreatedAt: string
}

export interface SecurityBlock {
  ID: string
  SubjectType: SecuritySubjectType
  Subject: string
  ClientIP: string
  APIKeyID: string
  APIKeyPrefix: string
  KeyFingerprint: string
  Reason: string
  FailureCount: number
  Status: SecurityBlockStatus
  BlockedUntil?: string | null
  ReleasedAt?: string | null
  CreatedAt: string
  UpdatedAt: string
}

export interface ListSecurityEventsParams {
  eventType?: string
  clientIP?: string
  apiKeyID?: string
  subjectType?: string
  limit?: number
}

export interface ListSecurityBlocksParams {
  status?: string
  limit?: number
}

export async function getSecuritySummary(): Promise<SecuritySummary> {
  const res = await request.get<SecuritySummary>('/security/summary')
  return res.data
}

export async function listSecurityEvents(params?: ListSecurityEventsParams): Promise<SecurityEvent[]> {
  const res = await request.get<{ events: SecurityEvent[] }>('/security/events', { params })
  return res.data.events
}

export async function listSecurityBlocks(params?: ListSecurityBlocksParams): Promise<SecurityBlock[]> {
  const res = await request.get<{ blocks: SecurityBlock[] }>('/security/blocks', { params })
  return res.data.blocks
}

export async function releaseSecurityBlock(id: string): Promise<SecurityBlock> {
  const res = await request.post<SecurityBlock>(`/security/blocks/${id}/release`)
  return res.data
}
