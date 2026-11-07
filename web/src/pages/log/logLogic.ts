// Pure helpers for Log & Report (task 026): the log viewer's URL state and
// the chart builder's query. The server (internal/logs) stays authoritative.
import { t } from '../../i18n'
import { EVENT_FIELDS, fieldsFor, type ChartType, type EventKind, type LogEvent, type LogFilter, type StatsBucket, type StatsQuery, type StatsSource, type TimeRange } from '../../api/stats'

// The tab labels live in the catalog as log.kind.<kind>; the "All" tab has no
// kind of its own, so its key is log.kind._all.
export const KINDS: { kind: EventKind | '' }[] = [
  { kind: '' },
  { kind: 'traffic' },
  { kind: 'ips' },
  { kind: 'dns' },
  { kind: 'webfilter' },
  { kind: 'appctrl' },
  { kind: 'system' },
]
export const RANGES: TimeRange[] = ['15m', '1h', '24h', '7d']

// Fields a chip can filter on (kind has its own tabs).
export const CHIP_FIELDS = EVENT_FIELDS.filter((f) => f !== 'kind') as readonly string[]

export interface LogState {
  kind: EventKind | ''
  q: string
  range: TimeRange
  chips: Record<string, string>
}

// parseLogParams reads /log?kind=&q=&from=-1h&<field>=… (the dashboard's
// drill-down links). Unknown kinds and fields are ignored.
export function parseLogParams(search: string): LogState {
  const p = new URLSearchParams(search)
  const kind = (KINDS.find((k) => k.kind === p.get('kind'))?.kind ?? '') as LogState['kind']
  const from = p.get('from') ?? ''
  const range = (RANGES.find((r) => '-' + r === from) ?? '1h') as TimeRange
  const chips: Record<string, string> = {}
  for (const f of CHIP_FIELDS) {
    const v = p.get(f)
    if (v) chips[f] = v
  }
  return { kind, q: p.get('q') ?? '', range, chips }
}

export function logParams(s: LogState): string {
  const p = new URLSearchParams()
  if (s.kind) p.set('kind', s.kind)
  if (s.q) p.set('q', s.q)
  if (s.range !== '1h') p.set('from', '-' + s.range)
  for (const [k, v] of Object.entries(s.chips)) p.set(k, v)
  return p.toString()
}

export function logFilter(s: LogState, cursor = 0): LogFilter {
  return { kind: s.kind || undefined, q: s.q || undefined, from: '-' + s.range, fields: s.chips, cursor, limit: 100 }
}

// A log table column is a field of LogEvent; the viewer names every header
// after its field (`colLabel` in LogViewer.tsx), so nothing here is user
// visible and a translation never has to be retyped.
export type LogColumn = keyof LogEvent

export function columnsFor(kind: LogState['kind']): LogColumn[] {
  const common: LogColumn[] = ['src_ip', 'dst_ip']
  switch (kind) {
    case 'traffic':
      return [...common, 'dst_port', 'proto', 'in_intf', 'out_intf', 'policy_id', 'action', 'bytes']
    case 'ips':
      return [...common, 'severity', 'signature', 'action']
    case 'dns':
    case 'webfilter':
      return ['src_ip', 'message', 'category', 'action']
    case 'appctrl':
      return ['src_ip', 'app', 'category', 'message', 'action']
    case 'system':
      return ['message']
    default:
      return ['kind', ...common, 'action', 'message']
  }
}

// summary is a one-line description for the "All" view's Details column.
export function summary(e: LogEvent): string {
  return e.message || e.signature || e.app || (e.dst_port ? `${e.proto ?? ''} ${e.dst_port}`.trim() : '') || ''
}

export const fmtTime = (unix: number) => new Date(unix * 1000).toLocaleString()

// --- chart builder ---

export interface Builder {
  title: string
  source: StatsSource
  metric: 'count' | 'bytes'
  group_by: string[]
  filters: Record<string, string>
  range: TimeRange
  bucket: StatsBucket | 'auto'
  chart: ChartType
}

export const emptyBuilder = (): Builder => ({
  title: '',
  source: 'events',
  metric: 'count',
  group_by: ['src_ip'],
  filters: {},
  range: '1h',
  bucket: 'none',
  chart: 'bar',
})

const AUTO: Record<TimeRange, StatsBucket> = { '15m': '1m', '1h': '5m', '24h': '1h', '7d': '1h' }

// validateBuilder returns problems that would make the query fail or the
// chart meaningless, keyed by field. The messages are shown under the field,
// so they read from the catalog; a field name stays as it is (it is data).
export function validateBuilder(b: Builder): Record<string, string> {
  const e: Record<string, string> = {}
  const allowed = fieldsFor(b.source)
  const badGroup = b.group_by.filter((f) => !allowed.includes(f))
  if (badGroup.length) e.group_by = t('chart.badGroup', { source: b.source, fields: badGroup.join(', ') })
  if (b.group_by.length > 2) e.group_by = t('chart.groupTooMany')
  const badFilter = Object.keys(b.filters).filter((f) => !allowed.includes(f))
  if (badFilter.length) e.filters = t('chart.badFilter', { source: b.source, fields: badFilter.join(', ') })
  const timed = b.bucket !== 'none'
  if ((b.chart === 'line' || b.chart === 'stacked') && !timed) e.bucket = t('chart.needBucket')
  if (b.chart === 'pie' && timed) e.bucket = t('chart.pieNoBucket')
  if (b.chart === 'pie' && b.group_by.length === 0) e.group_by = t('chart.pieNeedGroup')
  if (b.chart === 'stacked' && b.group_by.length === 0) e.group_by = t('chart.stackedNeedGroup')
  return e
}

// builderQuery is the stats query for the builder (from is set by the
// widget's range when saved; the preview uses the builder's range).
export function builderQuery(b: Builder): StatsQuery {
  const bucket = b.bucket === 'auto' ? AUTO[b.range] : b.bucket
  return {
    source: b.source,
    metric: b.metric,
    group_by: b.group_by.length ? b.group_by : undefined,
    filters: Object.keys(b.filters).length ? b.filters : undefined,
    bucket,
    limit: bucket === 'none' ? 20 : 1000,
  }
}

// defaultTitle names an untitled chart after its query. It is shown as the
// preview heading and the title field's placeholder, so the words come from the
// catalog; metric names and field names stay as they are (they are data).
export function defaultTitle(b: Builder): string {
  const what = b.metric === 'bytes' ? t('chart.metric.bytes') : t('chart.titleEvents')
  const by = b.group_by.length ? t('chart.groupedBy', { fields: b.group_by.join(', ') }) : ''
  const where = Object.entries(b.filters).map(([k, v]) => `${k}=${v}`).join(', ')
  return `${what}${by}${where ? ` (${where})` : ''}`
}
