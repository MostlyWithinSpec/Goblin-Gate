import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { getFeedStats, getPolicyStats, getSystemStats, statsQuery, type TimeRange, type Widget } from '../../api/stats'
import Chart from './Chart'
import { builtin, chartFor, formatValue, queryFor, toOption } from './dashboardLogic'

const RANGES: TimeRange[] = ['15m', '1h', '24h', '7d']
const REFRESH = 30_000

function useDark() {
  return typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
}

function Empty({ text = 'No data in this time range.' }: { text?: string }) {
  return <p className="flex h-full items-center justify-center text-sm text-gray-500">{text}</p>
}

function SystemBody() {
  const q = useQuery({ queryKey: ['stats', 'system'], queryFn: getSystemStats, refetchInterval: REFRESH })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError || !q.data) return <Empty text="System stats are not available." />
  const s = q.data
  const memPct = s.mem_total_bytes ? Math.round((s.mem_used_bytes / s.mem_total_bytes) * 100) : 0
  const up = `${Math.floor(s.uptime_seconds / 86400)}d ${Math.floor((s.uptime_seconds % 86400) / 3600)}h ${Math.floor((s.uptime_seconds % 3600) / 60)}m`
  const item = (label: string, value: string) => (
    <div>
      <p className="text-xs text-gray-500 dark:text-gray-400">{label}</p>
      <p className="text-lg font-medium text-gray-800 dark:text-white">{value}</p>
    </div>
  )
  return (
    <div className="grid grid-cols-2 gap-3 p-1">
      {item('CPU', `${s.cpu_percent.toFixed(1)}%`)}
      {item('Memory', `${memPct}% of ${formatValue('bytes', s.mem_total_bytes)}`)}
      {item('Uptime', up)}
      {item('Sessions', s.sessions.toLocaleString())}
    </div>
  )
}

function FeedBody({ dark }: { dark: boolean }) {
  const q = useQuery({ queryKey: ['stats', 'feeds'], queryFn: getFeedStats, refetchInterval: REFRESH })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text="Feed counters are not available." />
  const feeds = (q.data?.feeds ?? []).filter((f) => f.packets > 0)
  if (feeds.length === 0) return <Empty text="No threat feed hits since the last commit." />
  return <Chart dark={dark} option={toOption({ columns: ['feed', 'count'], rows: feeds.map((f) => [f.name, f.packets]) }, 'bar', dark)} />
}

function PolicyBody() {
  const navigate = useNavigate()
  const q = useQuery({ queryKey: ['stats', 'policies'], queryFn: getPolicyStats, refetchInterval: REFRESH })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text="Policy counters are not available." />
  const rows = [...(q.data?.policies ?? [])].sort((a, b) => b.packets - a.packets).slice(0, 10)
  if (rows.length === 0) return <Empty text="No policies have counters yet." />
  return (
    <table className="min-w-full text-sm text-gray-700 dark:text-gray-200">
      <thead className="text-left text-xs uppercase text-gray-500">
        <tr><th>Policy</th><th className="text-right">Packets</th><th className="text-right">Bytes</th></tr>
      </thead>
      <tbody>
        {rows.map((p) => (
          <tr key={p.id} className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700" onClick={() => navigate(`/log?kind=traffic&q=${p.id}`)}>
            <td>#{p.id}</td>
            <td className="text-right">{p.packets.toLocaleString()}</td>
            <td className="text-right">{formatValue('bytes', p.bytes)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function QueryBody({ widget, dark }: { widget: Widget; dark: boolean }) {
  const navigate = useNavigate()
  const query = queryFor(widget)
  const chart = chartFor(widget)
  const q = useQuery({
    queryKey: ['stats', 'query', JSON.stringify(query)],
    queryFn: () => statsQuery(query!),
    enabled: !!query,
    refetchInterval: REFRESH,
  })
  if (!query) return <Empty text="This widget has no query." />
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text={`Could not load: ${(q.error as Error).message}`} />
  const res = q.data!
  if (res.rows.length === 0) return <Empty />
  const drill = builtin(widget.type)?.drill
  const onClick = drill
    ? (name: string) => {
        const p = new URLSearchParams({ ...(drill.extra ?? {}), [drill.field]: name, from: '-' + widget.range })
        if (drill.kind) p.set('kind', drill.kind)
        navigate(`/log?${p}`)
      }
    : undefined
  if (chart === 'table') {
    const metric = res.columns[res.columns.length - 1]
    return (
      <table className="min-w-full text-sm text-gray-700 dark:text-gray-200">
        <thead className="text-left text-xs uppercase text-gray-500"><tr>{res.columns.map((c) => <th key={c}>{c}</th>)}</tr></thead>
        <tbody>
          {res.rows.map((r, i) => (
            <tr key={i}>{r.map((v, j) => <td key={j}>{j === r.length - 1 ? formatValue(metric, Number(v)) : String(v)}</td>)}</tr>
          ))}
        </tbody>
      </table>
    )
  }
  return <Chart dark={dark} option={toOption(res, chart as 'line' | 'bar' | 'stacked' | 'pie', dark)} onClick={onClick} />
}

// WidgetView is one dashboard tile: title, time range, remove, and its body.
export default function WidgetView({ widget, onChange, onRemove }: { widget: Widget; onChange: (patch: Partial<Widget>) => void; onRemove: () => void }) {
  const dark = useDark()
  const chart = chartFor(widget)
  const timeless = chart === 'system' || chart === 'feeds' || chart === 'policies'
  return (
    <div className="flex h-full flex-col rounded-lg bg-white p-3 shadow dark:bg-gray-800" data-testid={`widget-${widget.type}`}>
      <div className="widget-drag mb-2 flex cursor-move items-center gap-2">
        <h3 className="flex-1 truncate text-sm font-semibold text-gray-800 dark:text-white">{widget.title}</h3>
        {!timeless && (
          <select
            aria-label={`${widget.title} time range`}
            className="rounded border border-gray-300 bg-transparent px-1 text-xs dark:border-gray-600 dark:text-gray-200"
            value={widget.range}
            onMouseDown={(e) => e.stopPropagation()}
            onChange={(e) => onChange({ range: e.target.value as TimeRange })}
          >
            {RANGES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        )}
        <button type="button" aria-label={`Remove ${widget.title}`} className="text-gray-400 hover:text-red-500" onMouseDown={(e) => e.stopPropagation()} onClick={onRemove}>
          <X size={14} />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {chart === 'system' ? <SystemBody /> : chart === 'feeds' ? <FeedBody dark={dark} /> : chart === 'policies' ? <PolicyBody /> : <QueryBody widget={widget} dark={dark} />}
      </div>
    </div>
  )
}
