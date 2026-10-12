// Stats, logs and dashboard storage API (internal/api/stats_handlers.go,
// ui_handlers.go, internal/logs/query.go). Use these instead of mock data:
// every dashboard widget and the log viewer read from here.
import { apiClient } from './client'

// --- stats query (POST /api/v1/stats/query) ---

export type StatsSource = 'events' | 'rollup'
export type StatsMetric = 'count' | 'bytes'
export type StatsBucket = '1m' | '5m' | '1h' | 'none'

// Fields that can be grouped or filtered on, per source (server whitelist).
export const EVENT_FIELDS = [
  'kind', 'action', 'policy_id', 'src_ip', 'src_port', 'dst_ip', 'dst_port', 'proto',
  'in_intf', 'out_intf', 'app', 'category', 'signature', 'severity',
] as const
export const ROLLUP_FIELDS = ['kind', 'action', 'policy_id', 'in_intf', 'out_intf'] as const
export type StatsField = (typeof EVENT_FIELDS)[number]

export const fieldsFor = (source: StatsSource): readonly string[] => (source === 'rollup' ? ROLLUP_FIELDS : EVENT_FIELDS)

export interface StatsQuery {
  source?: StatsSource // default events
  metric?: StatsMetric // default count
  group_by?: string[]
  filters?: Record<string, string> // field -> exact value
  from?: string // "-1h" (default), "now", RFC 3339 or unix seconds
  to?: string // default now
  bucket?: StatsBucket // default none
  limit?: number // default 100, max 10000
}

// A bucketed result starts with a "time" column (unix seconds of the bucket
// start); the metric is always the last column.
export interface StatsResult {
  columns: string[]
  rows: (string | number)[][]
}

export const statsQuery = (q: StatsQuery) =>
  apiClient<StatsResult>('/api/v1/stats/query', { method: 'POST', body: JSON.stringify(q) })

// --- raw events (GET /api/v1/logs) ---

export type EventKind = 'traffic' | 'ips' | 'dns' | 'webfilter' | 'appctrl' | 'system'

export interface LogEvent {
  id: number
  time: number // unix seconds
  kind: EventKind
  action: string // allow | deny | alert | drop | accept
  policy_id?: string
  src_ip?: string
  src_port?: number
  dst_ip?: string
  dst_port?: number
  proto?: string
  in_intf?: string
  out_intf?: string
  bytes?: number
  app?: string
  category?: string
  signature?: string
  severity?: string
  message?: string
}

export interface LogFilter {
  kind?: EventKind
  q?: string // substring of message, ips, signature, app, category, policy_id
  from?: string
  to?: string
  limit?: number // default 100
  cursor?: number // next_cursor from the previous page
  fields?: Record<string, string> // exact matches on EVENT_FIELDS (not kind), e.g. {src_ip: '192.168.1.5'}
}

export interface LogPage {
  events: LogEvent[]
  next_cursor: number // 0 = no more pages
}

export function listLogs(f: LogFilter = {}): Promise<LogPage> {
  const p = new URLSearchParams()
  const { fields, ...rest } = f
  for (const [k, v] of Object.entries(rest)) {
    if (v !== undefined && v !== '' && v !== 0) p.set(k, String(v))
  }
  for (const [k, v] of Object.entries(fields ?? {})) {
    if (v !== '' && k !== 'kind') p.set(k, v)
  }
  const qs = p.toString()
  return apiClient<LogPage>('/api/v1/logs' + (qs ? '?' + qs : ''))
}

// --- live numbers ---

export interface PolicyStat {
  id: number
  packets: number
  bytes: number
  last_hit?: string // RFC 3339; absent until a hit was seen
}
export const getPolicyStats = () => apiClient<{ policies: PolicyStat[] }>('/api/v1/stats/policies')

export interface SystemStats {
  cpu_percent: number
  mem_total_bytes: number
  mem_used_bytes: number
  uptime_seconds: number
  sessions: number
}
export const getSystemStats = () => apiClient<SystemStats>('/api/v1/stats/system')

export interface FeedHit {
  name: string
  packets: number
  bytes: number
}
export const getFeedStats = () => apiClient<{ feeds: FeedHit[] }>('/api/v1/stats/feeds')

// --- dashboards (GET/PUT /api/v1/ui/dashboards, stored per user) ---

export type TimeRange = '15m' | '1h' | '24h' | '7d'
export type ChartType = 'line' | 'bar' | 'stacked' | 'pie' | 'table'

export interface Widget {
  id: string
  // a built-in widget name (system, bandwidth, top-talkers, ...) or "custom"
  type: string
  title: string
  range: TimeRange
  layout: { x: number; y: number; w: number; h: number }
  // custom widgets (chart builder): the query and how to draw it
  query?: StatsQuery
  chart?: ChartType
}

export interface Dashboard {
  id: string
  name: string
  widgets: Widget[]
}

export interface Dashboards {
  version: 1
  dashboards: Dashboard[]
}

export async function getDashboards(): Promise<Dashboards> {
  const d = await apiClient<Partial<Dashboards>>('/api/v1/ui/dashboards')
  return { version: 1, dashboards: d.dashboards ?? [] }
}

export const saveDashboards = (d: Dashboards) =>
  apiClient<{ status: string }>('/api/v1/ui/dashboards', { method: 'PUT', body: JSON.stringify(d) })

// rangeToFrom turns a widget time range into a stats query "from".
export const rangeToFrom = (r: TimeRange): string => '-' + r

// bucketFor picks a sensible bucket for a time range (about 15-170 points).
export const bucketFor = (r: TimeRange): StatsBucket => (r === '15m' ? '1m' : r === '1h' ? '5m' : '1h')
