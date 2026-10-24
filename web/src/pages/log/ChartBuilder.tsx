// Log & Report → Chart builder: compose a stats query (source, metric, group
// by, filters, bucket), preview it live and save it as a custom widget on a
// dashboard (stored in the dashboard JSON, /api/v1/ui/dashboards).
import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { fieldsFor, getDashboards, saveDashboards, statsQuery, type ChartType, type Dashboards, type StatsResult } from '../../api/stats'
import Chart from '../dashboard/Chart'
import { addCustom, defaultDashboards, formatValue, toOption } from '../dashboard/dashboardLogic'
import { RANGES, builderQuery, defaultTitle, emptyBuilder, validateBuilder, type Builder } from './logLogic'

const CHARTS: { value: ChartType; label: string }[] = [
  { value: 'bar', label: 'Bar' },
  { value: 'line', label: 'Line' },
  { value: 'stacked', label: 'Stacked bar' },
  { value: 'pie', label: 'Pie' },
  { value: 'table', label: 'Table' },
]
const input = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:border-gray-600 dark:bg-gray-700'

function Preview({ res, chart }: { res: StatsResult; chart: ChartType }) {
  if (res.rows.length === 0) return <p className="py-10 text-center text-sm text-gray-500">No data in this time range.</p>
  if (chart === 'table') {
    const metric = res.columns[res.columns.length - 1]
    return (
      <table className="min-w-full text-sm" data-testid="preview-table">
        <thead className="text-left text-xs uppercase text-gray-500"><tr>{res.columns.map((c) => <th key={c}>{c}</th>)}</tr></thead>
        <tbody>
          {res.rows.map((r, i) => <tr key={i}>{r.map((v, j) => <td key={j}>{j === r.length - 1 ? formatValue(metric, Number(v)) : String(v)}</td>)}</tr>)}
        </tbody>
      </table>
    )
  }
  return <div className="h-72"><Chart option={toOption(res, chart)} /></div>
}

export default function ChartBuilder() {
  const [b, setB] = useState<Builder>(emptyBuilder())
  const [res, setRes] = useState<StatsResult | null>(null)
  const [queryError, setQueryError] = useState<string | null>(null)
  const [dash, setDash] = useState<Dashboards | null>(null)
  const [target, setTarget] = useState('')
  const [saved, setSaved] = useState<string | null>(null)
  const [filterField, setFilterField] = useState('action')
  const [filterValue, setFilterValue] = useState('')
  const errors = validateBuilder(b)
  const valid = Object.keys(errors).length === 0
  const query = builderQuery(b)
  const qKey = JSON.stringify([query, b.range])

  const set = (patch: Partial<Builder>) => {
    setSaved(null)
    setB((x) => ({ ...x, ...patch }))
  }

  useEffect(() => {
    getDashboards()
      .then((d) => {
        const ds = d.dashboards.length ? d : defaultDashboards()
        setDash(ds)
        setTarget(ds.dashboards[0]?.id ?? '')
      })
      .catch(() => setDash(null))
  }, [])

  // live preview, debounced; stale answers are dropped
  useEffect(() => {
    if (!valid) return
    let live = true
    const t = setTimeout(() => {
      statsQuery({ ...query, from: '-' + b.range })
        .then((r) => live && (setRes(r), setQueryError(null)))
        .catch((e: Error) => live && setQueryError(e.message))
    }, 300)
    return () => {
      live = false
      clearTimeout(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [qKey, valid])

  const fields = fieldsFor(b.source)
  const err = (k: string) => errors[k] && <p role="alert" className="mt-1 text-xs text-red-600">{errors[k]}</p>

  const save = async () => {
    if (!dash || !valid) return
    const d = dash.dashboards.find((x) => x.id === target)
    if (!d) return
    const title = b.title.trim() || defaultTitle(b)
    const next: Dashboards = { ...dash, dashboards: dash.dashboards.map((x) => (x.id === d.id ? addCustom(x, title, query, b.chart, b.range) : x)) }
    try {
      await saveDashboards(next)
      setDash(next)
      setSaved(`Added "${title}" to ${d.name}.`)
    } catch (e) {
      setSaved(`Could not save: ${(e as Error).message}`)
    }
  }

  return (
    <div className="grid gap-4 lg:grid-cols-3">
      <div className="space-y-3 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300">
        <label className="block">
          Source
          <select
            aria-label="Source"
            className={input}
            value={b.source}
            onChange={(e) => {
              const source = e.target.value as Builder['source']
              const ok = fieldsFor(source)
              // drop group-by fields and filters the new source does not have
              set({ source, group_by: b.group_by.filter((f) => ok.includes(f)), filters: Object.fromEntries(Object.entries(b.filters).filter(([k]) => ok.includes(k))) })
            }}
          >
            <option value="events">Events (detailed, recent)</option>
            <option value="rollup">Rollups (traffic per minute, kept longer)</option>
          </select>
        </label>
        <label className="block">
          Metric
          <select aria-label="Metric" className={input} value={b.metric} onChange={(e) => set({ metric: e.target.value as Builder['metric'] })}>
            <option value="count">Count</option>
            <option value="bytes">Bytes</option>
          </select>
        </label>
        <fieldset>
          <legend>Group by (up to two)</legend>
          <div className="mt-1 grid grid-cols-1 sm:grid-cols-2 gap-x-2">
            {fields.map((f) => (
              <label key={f} className="flex items-center gap-1">
                <input type="checkbox" checked={b.group_by.includes(f)} onChange={(e) => set({ group_by: e.target.checked ? [...b.group_by, f] : b.group_by.filter((x) => x !== f) })} />
                {f}
              </label>
            ))}
          </div>
          {err('group_by')}
        </fieldset>
        <fieldset>
          <legend>Filters</legend>
          <div className="mt-1 flex gap-1">
            <select aria-label="Filter field" className={input + ' mt-0'} value={filterField} onChange={(e) => setFilterField(e.target.value)}>
              {fields.map((f) => <option key={f} value={f}>{f}</option>)}
            </select>
            <input aria-label="Filter value" className={input + ' mt-0'} value={filterValue} onChange={(e) => setFilterValue(e.target.value)} />
            <button
              type="button"
              className="rounded border border-gray-300 px-2 dark:border-gray-600"
              onClick={() => {
                if (!filterValue.trim()) return
                set({ filters: { ...b.filters, [filterField]: filterValue.trim() } })
                setFilterValue('')
              }}
            >
              Add
            </button>
          </div>
          <div className="mt-1 flex flex-wrap gap-1">
            {Object.entries(b.filters).map(([k, v]) => (
              <span key={k} className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs text-blue-800 dark:bg-blue-900 dark:text-blue-200">
                {k} = {v}
                <button
                  type="button"
                  aria-label={`Remove filter ${k}`}
                  onClick={() => {
                    const filters = { ...b.filters }
                    delete filters[k]
                    set({ filters })
                  }}
                >
                  <X size={12} />
                </button>
              </span>
            ))}
          </div>
          {err('filters')}
        </fieldset>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <label className="block">
            Time range
            <select aria-label="Time range" className={input} value={b.range} onChange={(e) => set({ range: e.target.value as Builder['range'] })}>
              {RANGES.map((r) => <option key={r} value={r}>Last {r}</option>)}
            </select>
          </label>
          <label className="block">
            Bucket
            <select aria-label="Bucket" className={input} value={b.bucket} onChange={(e) => set({ bucket: e.target.value as Builder['bucket'] })}>
              <option value="none">None (totals)</option>
              <option value="auto">Auto</option>
              <option value="1m">1 minute</option>
              <option value="5m">5 minutes</option>
              <option value="1h">1 hour</option>
            </select>
            {err('bucket')}
          </label>
        </div>
        <label className="block">
          Chart
          <select aria-label="Chart type" className={input} value={b.chart} onChange={(e) => set({ chart: e.target.value as ChartType })}>
            {CHARTS.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
          </select>
        </label>
      </div>

      <div className="space-y-3 lg:col-span-2">
        <div className="rounded bg-white p-4 shadow-sm dark:bg-gray-800 dark:text-gray-200">
          <h3 className="mb-2 text-sm font-semibold">{b.title.trim() || defaultTitle(b)}</h3>
          {!valid ? (
            <p className="py-10 text-center text-sm text-gray-500">Fix the highlighted settings to see a preview.</p>
          ) : queryError ? (
            <p role="alert" className="py-10 text-center text-sm text-red-600">{queryError}</p>
          ) : res ? (
            <Preview res={res} chart={b.chart} />
          ) : (
            <p className="py-10 text-center text-sm text-gray-500">Loading…</p>
          )}
        </div>
        <div className="flex flex-wrap items-end gap-2 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-300">
          <label className="block flex-1">
            Widget title
            <input aria-label="Widget title" className={input} placeholder={defaultTitle(b)} value={b.title} onChange={(e) => set({ title: e.target.value })} />
          </label>
          <label className="block">
            Dashboard
            <select aria-label="Dashboard" className={input} value={target} onChange={(e) => setTarget(e.target.value)} disabled={!dash}>
              {(dash?.dashboards ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
          </label>
          <button type="button" disabled={!valid || !dash} onClick={save} className="rounded bg-blue-600 px-3 py-1.5 text-white disabled:opacity-50">
            Save as widget
          </button>
          {saved && <p className="w-full text-sm text-gray-600 dark:text-gray-400" role="status">{saved}</p>}
        </div>
      </div>
    </div>
  )
}
