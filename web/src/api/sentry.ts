// Sentry plugin API client + Blocks API client.
// The Sentry plugin returns errors as {error: string} (not goblind's
// {error:{code,message}}), so we parse that shape explicitly and throw
// an Error with the message for the pages to display.
import { apiClient } from './client'
import { setPluginSettings, type SaveResult } from './plugins'

const BASE = '/api/v1/plugins/sentry/api'

// --- Types -----------------------------------------------------------------

export interface SentryStatus {
  mode: string
  counts: Record<string, number>
  rules: number
  watches: string[]
  cursor: number
  incidents: number
}

export interface AlertInfo {
  id: number
  time: number // unix seconds
  action: string
  src_ip: string
  src_port?: number
  dst_ip: string
  dst_port?: number
  proto?: string
  in_intf?: string
  signature: string
  signature_id?: string
  category?: string
  severity: number // 1 high .. 4 low, 0 unknown
}

export interface Enrichment {
  direction: 'inbound' | 'outbound' | 'lateral' | 'external'
  remote?: string
  local?: string
  local_name?: string
  local_interface?: string
  seen: number
  remote_seen: number
  watched: boolean
  blocked: boolean
  past_answers?: Record<string, number> // answer -> count
}

export interface Recommendation {
  tool: string
  target?: string
  minutes?: number
}

export type IncidentStatus = 'pending' | 'acted' | 'recorded' | 'ignored' | 'failed'

export interface Incident {
  id: number
  key: string
  status: IncidentStatus
  urgent?: boolean
  first: string // RFC3339
  last: string
  count: number
  alert: AlertInfo
  enrichment: Enrichment
  recommendation: Recommendation
  summary: string
  answer?: string
  answered_by?: string
}

export interface Decision {
  id: number
  incident_id: number
  time: string
  mode: string
  by: string // 'rule:<id>' | 'default' | 'admin:<user>' | 'model:<version>'
  rule_name?: string
  inputs: Enrichment
  action: Recommendation
  executed: boolean
  result: string
  // the decision model's advice, when it was asked
  model?: ModelAdvice
}

export interface ModelAdvice {
  model: string // e.g. 'v3 (knn)'
  action: Recommendation
  confidence: number // 0..1
  act: boolean // allowed to act at this confidence
  why: string // nearest past decisions or a short rationale
  used: boolean // the action ran (otherwise only suggested)
}

// --- G.O.B.L.I.N. decision model --------------------------------------------

export interface LabelStats {
  n: number
  predicted: number
  correct: number
  precision: number
  recall: number
}

export interface ThresholdStats {
  threshold: number
  acted: number
  wrong: number
  hidden_attacks: number
}

export interface ModelReport {
  kind: string // 'knn' | 'llm'
  model?: string
  created: string // RFC3339
  examples: number
  train: number
  test: number
  accuracy: number
  per_label: Record<string, LabelStats>
  confusion: Record<string, Record<string, number>>
  blocked_ignored: number
  ignored_blocked: number
  minutes_right: number
  thresholds: ThresholdStats[]
  novel: number
  novel_correct: number
  novel_acted: number
  invalid: number
  latency_ms: number
}

export interface ModelVersion {
  version: number // shown as v<number>
  kind: 'knn' | 'llm'
  created: string // RFC3339
  examples: number
  params?: string // e.g. 'k 7, half-life 30 days'
  report: ModelReport
  accepted?: string // RFC3339, when it was accepted
}

export interface ModelPolicy {
  use: 'off' | 'suggest' | 'act'
  thresholds: Record<string, number> // tool -> 0.5..1 (no key for blocks)
}

export interface ModelJob {
  kind: string
  name?: string
  started: string
  finished?: string
  version?: number // the new version when done
  error?: string
}

export interface ModelsState {
  current: number // 0 = no model in use
  previous: number // 0 = nothing to roll back to
  policy: ModelPolicy
  versions: ModelVersion[] // newest first
  job?: ModelJob
}

export interface LearnedExample {
  time: string // RFC3339
  alert: AlertInfo
  enrichment: Enrichment
  recommendation: Recommendation
  answer: 'block' | 'ignore' | 'approve' | 'always'
  action?: Recommendation
}

export interface Rule {
  id: number
  name: string
  enabled: boolean
  match: {
    signature?: string
    signature_id?: string
    category?: string
    max_severity?: number
    direction?: string
    remote?: string
    local?: string
  }
  action: {
    tool: string
    minutes?: number
  }
  auto?: boolean
  builtin?: boolean
  comment?: string
}

export interface Block {
  ip: string
  until: string // RFC3339
  reason: string
  source: string // 'admin' | 'plugin:<name>'
  created_by: string
  created: string
  expires_in: number // seconds
}

// --- Error handling ---------------------------------------------------------

class SentryError extends Error {
  status: number
  problems?: string[]
  constructor(message: string, status: number, problems?: string[]) {
    super(message)
    this.name = 'SentryError'
    this.status = status
    this.problems = problems
  }
}

export function isPluginNotRunning(e: unknown): boolean {
  return e instanceof SentryError && e.status === 503
}

// Parse a response body that may be {error: string, problems?: string[]}
async function parseError(response: Response): Promise<never> {
  let message = `HTTP ${response.status}`
  let problems: string[] | undefined
  try {
    const body = await response.json()
    if (typeof body?.error === 'string') {
      message = body.error
      if (Array.isArray(body.problems)) problems = body.problems
    } else if (body?.error?.message) {
      // goblind-style error shape
      message = body.error.message
    }
  } catch { /* keep default */ }
  throw new SentryError(message, response.status, problems)
}

// --- API functions ----------------------------------------------------------

export async function getSentryStatus(): Promise<SentryStatus> {
  const res = await fetch(`${BASE}/status`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getIncidents(status?: IncidentStatus, limit?: number): Promise<{ incidents: Incident[] }> {
  const params = new URLSearchParams()
  if (status) params.set('status', status)
  if (limit) params.set('limit', String(limit))
  const qs = params.toString()
  const url = `${BASE}/incidents${qs ? `?${qs}` : ''}`
  const res = await fetch(url, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getIncident(id: number): Promise<{ incident: Incident; decisions: Decision[] }> {
  const res = await fetch(`${BASE}/incidents/${id}`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export type Answer = 'block' | 'ignore' | 'approve' | 'always'

export async function answerIncident(id: number, answer: Answer, minutes?: number): Promise<{ incident: Incident; decision: Decision }> {
  const body: Record<string, unknown> = { answer }
  if (minutes !== undefined) body.minutes = minutes
  const res = await fetch(`${BASE}/incidents/${id}/answer`, {
    method: 'POST',
    headers: { ...csrfHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getDecisions(limit?: number): Promise<{ decisions: Decision[] }> {
  const params = new URLSearchParams()
  if (limit) params.set('limit', String(limit))
  const qs = params.toString()
  const url = `${BASE}/decisions${qs ? `?${qs}` : ''}`
  const res = await fetch(url, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getRules(): Promise<{ rules: Rule[] }> {
  const res = await fetch(`${BASE}/rules`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function putRules(rules: Rule[]): Promise<{ rules: Rule[] }> {
  const res = await fetch(`${BASE}/rules`, {
    method: 'PUT',
    headers: { ...csrfHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ rules })
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

// Mode change goes through the plugin settings endpoint (candidate config).
export async function setSentryMode(mode: string): Promise<SaveResult> {
  // Fetch current settings first so we only change mode.
  const { getPlugins } = await import('./plugins')
  const plugins = await getPlugins()
  const sentry = plugins.plugins.find((p) => p.name === 'sentry')
  const current: Record<string, unknown> = sentry?.settings ?? {}
  return setPluginSettings('sentry', { ...current, mode })
}

// --- G.O.B.L.I.N. decision model --------------------------------------------

export async function getModels(): Promise<ModelsState> {
  const res = await fetch(`${BASE}/models`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export interface TrainInput {
  kind?: 'knn' | 'llm'
  endpoint?: string // for llm: llama-server URL
  name?: string
}

// Start a training job. Returns the job; poll getModels() until it finishes.
export async function trainModel(input: TrainInput = {}): Promise<{ job: ModelJob }> {
  const res = await fetch(`${BASE}/models/train`, {
    method: 'POST',
    headers: { ...csrfHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(input)
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getModel(version: number): Promise<ModelVersion> {
  const res = await fetch(`${BASE}/models/${version}`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function acceptModel(version: number): Promise<{ current: number }> {
  const res = await fetch(`${BASE}/models/${version}/accept`, {
    method: 'POST',
    headers: csrfHeaders()
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function rollbackModel(): Promise<{ current: number }> {
  const res = await fetch(`${BASE}/models/rollback`, {
    method: 'POST',
    headers: csrfHeaders()
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

// Replace the whole policy (use + thresholds). Thresholds are 0.5..1 per tool;
// there is no threshold for blocks.
export async function putModelPolicy(policy: ModelPolicy): Promise<ModelPolicy> {
  const res = await fetch(`${BASE}/models/policy`, {
    method: 'PUT',
    headers: { ...csrfHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(policy)
  })
  if (!res.ok) return parseError(res)
  return res.json()
}

export async function getExamples(): Promise<{ examples: LearnedExample[] }> {
  const res = await fetch(`${BASE}/models/examples`, { headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
  return res.json()
}

// Remove one learned example (the admin's answer it was trained on).
export async function deleteExample(alertId: number): Promise<void> {
  const res = await fetch(`${BASE}/models/examples/${alertId}`, { method: 'DELETE', headers: csrfHeaders() })
  if (!res.ok) return parseError(res)
}

// --- Blocks API (goblind native, /api/v1/blocks) ----------------------------

export async function getBlocks(): Promise<{ blocks: Block[] }> {
  return apiClient<{ blocks: Block[] }>('/api/v1/blocks')
}

export interface CreateBlockInput {
  ip: string
  minutes: number // 1-10080
  reason: string // required, <=500
  allow_internal?: boolean
}

export async function createBlock(input: CreateBlockInput): Promise<Block> {
  return apiClient<Block>('/api/v1/blocks', { method: 'POST', body: JSON.stringify(input) })
}

export async function deleteBlock(ip: string): Promise<void> {
  await apiClient<void>(`/api/v1/blocks/${encodeURIComponent(ip)}`, { method: 'DELETE' })
}

// --- CSRF helper (mirrors client.ts but for raw fetch) ----------------------

function csrfHeaders(): Record<string, string> {
  const headers: Record<string, string> = {}
  try {
    const m = document.cookie.match(/(?:^|;\s*)gg_csrf=([^;]*)/)
    if (m && m[1]) headers['X-CSRF-Token'] = decodeURIComponent(m[1])
  } catch { /* ignore */ }
  return headers
}
