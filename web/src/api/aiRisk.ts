import { requestData } from './request'

export type RiskLevel = 'low' | 'medium' | 'high' | 'blocked'
export type RiskStatus = 'pending' | 'rated' | 'needs_review' | 'stale' | 'error' | 'removed'
export type ReviewReason =
  | 'insufficient_evidence'
  | 'ambiguous_scope'
  | 'conflicting_signals'
  | 'low_confidence'
  | 'below_rule_floor'
  | 'legacy_ai_request'
export type RiskProfile = 'legacy_unrestricted' | 'readonly' | 'standard' | 'privileged'
export type APIStyle = 'chat_completions' | 'responses'

export interface AIProvider {
  id: string
  name: string
  baseUrl: string
  apiStyle: APIStyle
  model: string
  hasApiKey: boolean
  apiKeyMasked?: string
  enabled: boolean
  active: boolean
  timeoutS: number
  batchSize: number
  maxConcurrency: number
  autoAssess: boolean
  createdAt: string
  updatedAt: string
}

export interface ProviderInput {
  name: string
  baseUrl: string
  apiStyle: APIStyle
  model: string
  apiKey?: string
  clearApiKey?: boolean
  enabled: boolean
  timeoutS: number
  batchSize: number
  maxConcurrency: number
  autoAssess: boolean
}

export interface ToolRiskAssessment {
  id: string
  upstreamId: string
  upstreamName?: string
  originalName: string
  exposedName: string
  description: string
  descriptionZh: string
  schemaFingerprint: string
  deterministicFloor: RiskLevel
  aiLevel?: RiskLevel
  aiTags: string[]
  aiConfidence?: number
  aiReason: string
  reviewReasons: ReviewReason[]
  providerName: string
  model: string
  status: RiskStatus
  lastError: string
  manualLevel?: RiskLevel
  manualTags: string[]
  manualReason: string
  manualForceDowngrade: boolean
  manualConfirmed: boolean
  effectiveLevel: RiskLevel
  assessedAt?: string
  updatedAt: string
}

export interface RiskToolPage {
  items: ToolRiskAssessment[]
  total: number
  page: number
  pageSize: number
  summary: RiskSummary
}

export interface RiskSummary {
  total: number
  low: number
  medium: number
  high: number
  blocked: number
  needsReview: number
}

export interface AssessmentJob {
  id: string
  providerId: string
  scope: string
  scopePayload: Record<string, unknown>
  status: 'queued' | 'running' | 'completed' | 'partial' | 'failed' | 'cancelled'
  requestedCount: number
  processedCount: number
  successCount: number
  reviewCount: number
  failureCount: number
  retryCount: number
  splitCount: number
  errorCounts: Record<string, number>
  lastError: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

export interface ProviderStatus {
  providers: AIProvider[]
  encryptionReady: boolean
}

export const getProviderStatus = async (): Promise<ProviderStatus> => {
  const status = await requestData<{
    providers: AIProvider[] | null
    encryptionReady: boolean
  }>({ url: '/ai-risk/providers' })
  return { providers: status.providers ?? [], encryptionReady: status.encryptionReady }
}
export const createProvider = (data: ProviderInput) =>
  requestData<AIProvider>({ url: '/ai-risk/providers', method: 'POST', data })
export const updateProvider = (id: string, data: ProviderInput) =>
  requestData<AIProvider>({
    url: `/ai-risk/providers/${encodeURIComponent(id)}`,
    method: 'PUT',
    data,
  })
export const deleteProvider = (id: string) =>
  requestData<void>({ url: `/ai-risk/providers/${encodeURIComponent(id)}`, method: 'DELETE' })
export const activateProvider = (id: string) =>
  requestData<{ id: string; active: boolean }>({
    url: `/ai-risk/providers/${encodeURIComponent(id)}/activate`,
    method: 'POST',
  })
export const testProvider = (id: string) =>
  requestData<{ ok: boolean; latencyMs: number }>({
    url: `/ai-risk/providers/${encodeURIComponent(id)}/test`,
    method: 'POST',
  })

export function listRiskTools(params: Record<string, string | number | boolean | undefined>) {
  return requestData<RiskToolPage>({ url: '/ai-risk/tools', params })
}
export const reconcileRiskCatalog = () =>
  requestData<{ added: number; changed: number; removed: number; current: number }>({
    url: '/ai-risk/reconcile',
    method: 'POST',
  })
export const queueAssessment = (limit = 500) =>
  requestData<AssessmentJob>({ url: '/ai-risk/assess', method: 'POST', params: { limit } })
export const queueReviewAssessment = (limit = 500) =>
  requestData<AssessmentJob>({ url: '/ai-risk/assess/review', method: 'POST', params: { limit } })
export const listAssessmentJobs = async () =>
  (await requestData<{ jobs: AssessmentJob[] | null }>({ url: '/ai-risk/jobs' })).jobs ?? []
export const cancelAssessmentJob = (id: string) =>
  requestData<{ id: string; status: string }>({
    url: `/ai-risk/jobs/${encodeURIComponent(id)}/cancel`,
    method: 'POST',
  })
export const reassessRiskTool = (item: ToolRiskAssessment) =>
  requestData<ToolRiskAssessment>({
    url: `/ai-risk/tools/${encodeURIComponent(item.upstreamId)}/${encodeURIComponent(item.originalName)}/reassess`,
    method: 'POST',
  })

export const setManualOverride = (
  item: ToolRiskAssessment,
  data: { level: RiskLevel; tags: string[]; reason: string; force: boolean },
) =>
  requestData<ToolRiskAssessment>({
    url: `/ai-risk/tools/${encodeURIComponent(item.upstreamId)}/${encodeURIComponent(item.originalName)}/manual-override`,
    method: 'PUT',
    data,
  })
export const clearManualOverride = (item: ToolRiskAssessment) =>
  requestData<ToolRiskAssessment>({
    url: `/ai-risk/tools/${encodeURIComponent(item.upstreamId)}/${encodeURIComponent(item.originalName)}/manual-override`,
    method: 'DELETE',
  })

export const bulkManualOverride = (
  items: ToolRiskAssessment[],
  data: { level: RiskLevel; tags: string[]; reason: string; force: boolean },
) =>
  requestData<{ items: ToolRiskAssessment[]; updated: number }>({
    url: '/ai-risk/tools/bulk-override',
    method: 'POST',
    data: {
      ...data,
      items: items.map((item) => ({
        upstreamId: item.upstreamId,
        originalName: item.originalName,
      })),
    },
  })
