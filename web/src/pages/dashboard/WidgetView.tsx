import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { getFeedStats, getPolicyStats, getSystemStats, statsQuery, type TimeRange, type Widget } from '../../api/stats'
import { getGatewayStatus, getWANStatus } from '../../api/gateways'
import { formatRate, listFlowDevices } from '../../api/flows'
import { countBySeverity, getChecks, scoreTone } from '../../api/checks'
import { getCandidate, getUPSStatus } from '../../api/objects'
import { ChargeBar, StateBadge, runtimeText } from '../system/UPS'
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
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 p-1">
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
  const cfg = useQuery({ queryKey: ['candidate'], queryFn: getCandidate })
  const names = new Map((cfg.data?.policies ?? []).map((p) => [p.id, p.name]))
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
            <td className="max-w-0 truncate" title={names.get(p.id) ?? ''}>
              <span className="text-gray-500">#{p.id}</span> {names.get(p.id) ?? ''}
            </td>
            <td className="text-right">{p.packets.toLocaleString()}</td>
            <td className="text-right">{formatValue('bytes', p.bytes)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// WANBody is the WAN status of a box without multi-WAN gateways: each WAN
// interface's link, address and the next hop of its default route.
function WANBody() {
  const q = useQuery({ queryKey: ['wan', 'status'], queryFn: getWANStatus, refetchInterval: REFRESH })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text="WAN status is not available." />
  const wans = q.data ?? []
  if (wans.length === 0) return <Empty text="No WAN interface is configured." />
  return (
    <ul className="divide-y divide-gray-100 dark:divide-gray-700">
      {wans.map((w) => (
        <li key={w.name} className="flex flex-wrap items-center gap-2 py-1.5 text-sm">
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${w.up ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
            {w.up ? 'up' : 'down'}
          </span>
          <span className="font-medium text-gray-800 dark:text-gray-100">{w.name}</span>
          <span className="text-xs text-gray-500">{w.device}</span>
          <span className="ml-auto text-xs text-gray-500">
            {w.addresses[0] ?? 'no address'}
            {w.gateway ? ` · via ${w.gateway}` : ' · no default route'}
          </span>
        </li>
      ))}
    </ul>
  )
}

function GatewayBody() {
  const q = useQuery({ queryKey: ['gateways', 'status'], queryFn: getGatewayStatus, refetchInterval: REFRESH })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text="Gateway status is not available." />
  const gateways = q.data?.gateways ?? []
  // a single WAN has no monitored gateways: show the WAN itself
  if (gateways.length === 0) return <WANBody />
  const badge = (state: string) =>
    state === 'up' ? 'bg-green-100 text-green-700' : state === 'down' ? 'bg-red-100 text-red-700' : 'bg-gray-100 text-gray-600'
  return (
    <ul className="divide-y divide-gray-100 dark:divide-gray-700">
      {gateways.map((g) => (
        <li key={g.name} className="flex items-center gap-2 py-1.5 text-sm">
          <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${badge(g.state)}`}>{g.state}</span>
          <span className="flex-1 truncate font-medium text-gray-800 dark:text-gray-100">{g.name}</span>
          {g.state === 'up' && (
            <span className="text-xs text-gray-500">
              {g.latency_ms} ms · {g.loss_percent}% loss
            </span>
          )}
        </li>
      ))}
    </ul>
  )
}

// LiveTalkersBody is the live "Top talkers" widget (task 079): the top 5
// devices by current bandwidth from /api/v1/flows/devices, refreshed every 3 s.
function LiveTalkersBody() {
  const q = useQuery({ queryKey: ['flows', 'devices'], queryFn: listFlowDevices, refetchInterval: 3_000, retry: false })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError) return <Empty text="Live flows are read on the firewall itself." />
  const devices = (q.data ?? []).slice(0, 5)
  if (devices.length === 0) return <Empty text="No active flows yet." />
  return (
    <table className="min-w-full text-sm text-gray-700 dark:text-gray-200">
      <thead className="text-left text-xs uppercase text-gray-500">
        <tr><th>Device</th><th className="text-right">Download</th><th className="text-right">Upload</th></tr>
      </thead>
      <tbody>
        {devices.map((d) => (
          <tr key={d.ip} data-testid={`talker-${d.ip}`}>
            <td className="truncate">{d.name || d.ip}</td>
            <td className="text-right font-mono text-xs" data-testid={`talker-down-${d.ip}`}>{formatRate(d.rx_bps)}</td>
            <td className="text-right font-mono text-xs">{formatRate(d.tx_bps)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// ChecksBody is the "Goblin Checks" widget (task 109): the score and how many
// high findings there are, clicking the tile opens the checks page.
function ChecksBody() {
  const navigate = useNavigate()
  const q = useQuery({ queryKey: ['checks'], queryFn: getChecks, refetchInterval: 60_000, retry: false })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError || !q.data) return <Empty text="The checks are not available." />
  const report = q.data
  const high = countBySeverity(report.findings, 'high')
  const tone = scoreTone(report.score)
  const colour =
    tone === 'success'
      ? 'text-green-600 dark:text-green-400'
      : tone === 'warning'
        ? 'text-amber-600 dark:text-amber-400'
        : 'text-red-600 dark:text-red-400'
  return (
    <button
      type="button"
      onClick={() => navigate('/checks')}
      className="flex h-full w-full flex-col items-start justify-center gap-1 p-1 text-left"
    >
      <span className={`text-4xl font-bold ${colour}`} data-testid="widget-checks-score">
        {report.score}
      </span>
      <span className="text-sm text-gray-600 dark:text-gray-300" data-testid="widget-checks-high">
        {high === 0 ? 'No high findings' : `${high} high finding${high === 1 ? '' : 's'}`}
      </span>
      <span className="text-xs text-gray-500 dark:text-gray-400">
        {report.findings.length} finding{report.findings.length === 1 ? '' : 's'} in total · open Goblin Checks
      </span>
    </button>
  )
}

// UpsBody is the UPS tile: the power state, the charge bar and the runtime left,
// reading the same endpoint and badge as the System → UPS page.
function UpsBody() {
  const navigate = useNavigate()
  const q = useQuery({ queryKey: ['ups-status'], queryFn: getUPSStatus, refetchInterval: 15_000, retry: false })
  if (q.isLoading) return <Empty text="Loading…" />
  if (q.isError || !q.data) return <Empty text="The UPS status is not available." />
  const s = q.data
  return (
    <button
      type="button"
      onClick={() => navigate('/system/ups')}
      className="flex h-full w-full flex-col items-start justify-center gap-2 p-1 text-left"
    >
      <StateBadge state={s.state} connected={s.connected} enabled={s.enabled} />
      <div className="w-full">
        <ChargeBar charge={s.charge} state={s.state} />
      </div>
      <span className="text-xs text-gray-500 dark:text-gray-400" data-testid="widget-ups-runtime">
        Runtime {runtimeText(s.runtime_seconds)}
        {typeof s.load === 'number' ? ` · load ${Math.round(s.load)} %` : ''}
      </span>
    </button>
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
  const timeless = chart === 'system' || chart === 'feeds' || chart === 'policies' || chart === 'gateways' || chart === 'live-talkers' || chart === 'checks' || chart === 'ups'
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
        {chart === 'system' ? <SystemBody /> : chart === 'feeds' ? <FeedBody dark={dark} /> : chart === 'policies' ? <PolicyBody /> : chart === 'gateways' ? <GatewayBody /> : chart === 'live-talkers' ? <LiveTalkersBody /> : chart === 'checks' ? <ChecksBody /> : chart === 'ups' ? <UpsBody /> : <QueryBody widget={widget} dark={dark} />}
      </div>
    </div>
  )
}
