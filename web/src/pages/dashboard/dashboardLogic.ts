// Dashboard model helpers (task 025): built-in widgets, default dashboards,
// edits, and turning stats query results into ECharts options. Pure
// functions; the page and widgets use them.
import type { ChartType, Dashboard, Dashboards, StatsQuery, StatsResult, TimeRange, Widget } from '../../api/stats'
import { bucketFor, rangeToFrom } from '../../api/stats'

export interface Builtin {
  type: string
  title: string
  chart: ChartType | 'system' | 'feeds' | 'policies' | 'gateways' | 'live-talkers'
  size: { w: number; h: number }
  // the stats query for a time range (query-based widgets only)
  query?: (r: TimeRange) => StatsQuery
  // the log viewer filter a click on a row/bar opens: field = clicked value
  drill?: { kind?: string; field: string; extra?: Record<string, string> }
}

export const BUILTINS: Builtin[] = [
  { type: 'system', title: 'System', chart: 'system', size: { w: 4, h: 3 } },
  {
    type: 'bandwidth', title: 'Bandwidth by interface', chart: 'line', size: { w: 8, h: 4 },
    query: (r) => ({ metric: 'bytes', group_by: ['in_intf'], bucket: bucketFor(r), from: rangeToFrom(r), limit: 2000 }),
  },
  {
    type: 'top-talkers', title: 'Top talkers', chart: 'bar', size: { w: 4, h: 4 },
    query: (r) => ({ metric: 'bytes', group_by: ['src_ip'], from: rangeToFrom(r), limit: 10 }),
    drill: { field: 'src_ip' },
  },
  {
    type: 'top-destinations', title: 'Top destinations', chart: 'bar', size: { w: 4, h: 4 },
    query: (r) => ({ metric: 'bytes', group_by: ['dst_ip'], from: rangeToFrom(r), limit: 10 }),
    drill: { field: 'dst_ip' },
  },
  {
    type: 'top-blocked', title: 'Top blocked (by policy)', chart: 'bar', size: { w: 4, h: 4 },
    query: (r) => ({ metric: 'count', filters: { action: 'deny' }, group_by: ['policy_id'], from: rangeToFrom(r), limit: 10 }),
    drill: { field: 'policy_id', extra: { action: 'deny' } },
  },
  {
    type: 'ips-severity', title: 'IPS events by severity', chart: 'stacked', size: { w: 8, h: 4 },
    query: (r) => ({ metric: 'count', filters: { kind: 'ips' }, group_by: ['severity'], bucket: bucketFor(r), from: rangeToFrom(r), limit: 2000 }),
    drill: { kind: 'ips', field: 'severity' },
  },
  { type: 'feed-hits', title: 'Threat feed hits', chart: 'feeds', size: { w: 4, h: 4 } },
  { type: 'policy-hits', title: 'Policy hit leaderboard', chart: 'policies', size: { w: 4, h: 4 } },
  { type: 'gateways', title: 'WAN status', chart: 'gateways', size: { w: 4, h: 4 } },
  { type: 'live-top-talkers', title: 'Top talkers (live)', chart: 'live-talkers', size: { w: 4, h: 4 } },
]

export const builtin = (type: string) => BUILTINS.find((b) => b.type === type)

let seq = 0
export const newId = (prefix: string) => `${prefix}-${Date.now().toString(36)}-${(seq++).toString(36)}`

// placeWidget puts a widget of size w x h at the first free spot of a
// 12-column grid, row by row.
export function placeWidget(existing: Widget[], w: number, h: number): Widget['layout'] {
  const taken = (x: number, y: number) =>
    existing.some((o) => x < o.layout.x + o.layout.w && x + w > o.layout.x && y < o.layout.y + o.layout.h && y + h > o.layout.y)
  for (let y = 0; ; y++) {
    for (let x = 0; x + w <= 12; x++) {
      if (!taken(x, y)) return { x, y, w, h }
    }
  }
}

export function addBuiltin(d: Dashboard, type: string): Dashboard {
  const b = builtin(type)
  if (!b) return d
  const widget: Widget = { id: newId('w'), type, title: b.title, range: '1h', layout: placeWidget(d.widgets, b.size.w, b.size.h) }
  return { ...d, widgets: [...d.widgets, widget] }
}

export function addCustom(d: Dashboard, title: string, query: StatsQuery, chart: ChartType, range: TimeRange = '1h'): Dashboard {
  const widget: Widget = { id: newId('w'), type: 'custom', title, range, query, chart, layout: placeWidget(d.widgets, 6, 4) }
  return { ...d, widgets: [...d.widgets, widget] }
}

export function defaultDashboards(): Dashboards {
  let main: Dashboard = { id: 'main', name: 'Main', widgets: [] }
  for (const b of BUILTINS) main = addBuiltin(main, b.type)
  return { version: 1, dashboards: [main] }
}

export const updateWidget = (d: Dashboard, id: string, patch: Partial<Widget>): Dashboard => ({
  ...d,
  widgets: d.widgets.map((w) => (w.id === id ? { ...w, ...patch } : w)),
})

export const removeWidget = (d: Dashboard, id: string): Dashboard => ({ ...d, widgets: d.widgets.filter((w) => w.id !== id) })

// applyLayout copies grid positions back into the widgets.
export function applyLayout(d: Dashboard, layout: { i: string; x: number; y: number; w: number; h: number }[]): Dashboard {
  const pos = new Map(layout.map((l) => [l.i, l]))
  return {
    ...d,
    widgets: d.widgets.map((w) => {
      const l = pos.get(w.id)
      return l ? { ...w, layout: { x: l.x, y: l.y, w: l.w, h: l.h } } : w
    }),
  }
}

// queryFor is the stats query a widget runs, or undefined for widgets with
// their own endpoint (system, feeds, policies).
export function queryFor(w: Widget): StatsQuery | undefined {
  if (w.type === 'custom') return w.query ? { ...w.query, from: rangeToFrom(w.range) } : undefined
  return builtin(w.type)?.query?.(w.range)
}

export function chartFor(w: Widget): ChartType | 'system' | 'feeds' | 'policies' | 'gateways' | 'live-talkers' {
  if (w.type === 'custom') return w.chart ?? 'table'
  return builtin(w.type)?.chart ?? 'table'
}

// formatValue makes byte counts readable.
export function formatValue(metric: string, v: number): string {
  if (metric !== 'bytes') return v.toLocaleString()
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

const timeLabel = (unix: number) => {
  const d = new Date(unix * 1000)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

/* eslint-disable @typescript-eslint/no-explicit-any */
// toOption converts a stats result into an ECharts option. Bucketed results
// ("time" first column) become one series per group over time; others a
// single bar/pie series over the first group column.
export function toOption(res: StatsResult, chart: ChartType, dark = false): Record<string, any> {
  const text = dark ? '#d1d5db' : '#374151'
  const metric = res.columns[res.columns.length - 1]
  const base = { textStyle: { color: text }, tooltip: { trigger: chart === 'pie' ? 'item' : 'axis' }, grid: { left: 60, right: 16, top: 28, bottom: 32 } }
  if (res.columns[0] === 'time') {
    const times = [...new Set(res.rows.map((r) => Number(r[0])))].sort((a, b) => a - b)
    const groupCols = res.columns.length - 2
    const key = (r: (string | number)[]) => (groupCols > 0 ? r.slice(1, -1).join(' / ') : metric)
    const groups = [...new Set(res.rows.map(key))]
    const series = groups.map((g) => {
      const byTime = new Map(res.rows.filter((r) => key(r) === g).map((r) => [Number(r[0]), Number(r[r.length - 1])]))
      return {
        name: g || '(none)',
        type: chart === 'line' ? 'line' : 'bar',
        stack: chart === 'stacked' ? 'total' : undefined,
        showSymbol: false,
        data: times.map((t) => byTime.get(t) ?? 0),
      }
    })
    return {
      ...base,
      legend: { textStyle: { color: text }, type: 'scroll' },
      xAxis: { type: 'category', data: times.map(timeLabel), axisLabel: { color: text } },
      yAxis: { type: 'value', axisLabel: { color: text, formatter: (v: number) => formatValue(metric, v) } },
      series,
    }
  }
  const labels = res.rows.map((r) => (res.columns.length > 1 ? r.slice(0, -1).join(' / ') : metric) || '(none)')
  const values = res.rows.map((r) => Number(r[r.length - 1]))
  if (chart === 'pie') {
    return { ...base, series: [{ type: 'pie', radius: '65%', data: labels.map((name, i) => ({ name, value: values[i] })), label: { color: text } }] }
  }
  return {
    ...base,
    grid: { left: 120, right: 16, top: 8, bottom: 24 },
    xAxis: { type: 'value', axisLabel: { color: text, formatter: (v: number) => formatValue(metric, v) } },
    yAxis: { type: 'category', inverse: true, data: labels, axisLabel: { color: text } },
    series: [{ type: 'bar', data: values, itemStyle: { color: '#3b82f6' } }],
  }
}
