// Log & Report → Logs: events from GET /api/v1/logs, newest first. Kind tabs,
// a filter bar (search, time range, field = value chips) kept in the URL so
// dashboard drill-downs land here, cursor paging with infinite scroll, and a
// detail drawer per event.
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { Filter, X } from 'lucide-react'
import { listLogs, type LogEvent } from '../../api/stats'
import { geoipLookup, getGeoIP } from '../../api/objects'
import { Badge } from '../../components/Badge'
import { EditDrawer } from '../../components/EditDrawer'
import { CHIP_FIELDS, KINDS, RANGES, columnsFor, fmtTime, logFilter, logParams, parseLogParams, summary, type LogState } from './logLogic'

const actionVariant = (a: string) => (a === 'deny' || a === 'drop' ? 'error' : a === 'alert' ? 'warning' : a === 'allow' || a === 'accept' ? 'success' : 'secondary')
const input = 'rounded border border-gray-300 px-2 py-1 text-sm dark:border-gray-600 dark:bg-gray-700'

// CountryRow: looks up the country for an IP via /api/v1/geoip/lookup, but only
// when the GeoIP database is loaded. Renders nothing otherwise.
function CountryRow({ ip }: { ip: string }) {
  const [country, setCountry] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getGeoIP()
      .then((r) => {
        if (!r.status.loaded) return
        return geoipLookup(ip).then((res) => {
          if (!cancelled) setCountry(res.country || null)
        })
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [ip])

  if (country === null) return null
  return (
    <>
      <dt className="text-gray-500">src_country</dt>
      <dd className="sm:col-span-2">{country}</dd>
    </>
  )
}

function cell(e: LogEvent, f: keyof LogEvent): React.ReactNode {
  if (f === 'action') return <Badge variant={actionVariant(e.action)}>{e.action}</Badge>
  if (f === 'message' && !e.message) return summary(e)
  const v = e[f]
  return v === undefined || v === '' ? '' : String(v)
}

// Builds the /policy?simulate=1… query that opens the policy simulator with a
// denied event's flow prefilled.
function simulateQuery(e: LogEvent): string {
  const q = new URLSearchParams()
  q.set('simulate', '1')
  if (e.src_ip) q.set('src_ip', e.src_ip)
  if (e.dst_ip) q.set('dst_ip', e.dst_ip)
  if (e.proto === 'tcp' || e.proto === 'udp' || e.proto === 'icmp') q.set('protocol', e.proto)
  if (e.dst_port != null && e.dst_port > 0) q.set('dst_port', String(e.dst_port))
  if (e.in_intf) q.set('src_intf', e.in_intf)
  q.set('at', new Date(e.time * 1000).toISOString())
  return q.toString()
}

export default function LogViewer() {
  const location = useLocation()
  const navigate = useNavigate()
  const state = useMemo(() => parseLogParams(location.search), [location.search])
  const key = logParams(state)
  const setState = (s: LogState) => navigate({ search: logParams(s) })

  const [events, setEvents] = useState<LogEvent[]>([])
  const [next, setNext] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [detail, setDetail] = useState<LogEvent | null>(null)
  const [q, setQ] = useState(state.q)
  const [chipField, setChipField] = useState<string>('src_ip')
  const [chipValue, setChipValue] = useState('')
  const gen = useRef(0)

  // first page whenever the filters change; stale answers are dropped
  useEffect(() => {
    const g = ++gen.current
    setLoading(true)
    setError(null)
    setQ(state.q)
    listLogs(logFilter(state))
      .then((p) => {
        if (g !== gen.current) return
        setEvents(p.events ?? [])
        setNext(p.next_cursor)
      })
      .catch((e: Error) => g === gen.current && setError(e.message))
      .finally(() => g === gen.current && setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  const loadMore = useCallback(() => {
    if (!next || loading) return
    const g = gen.current
    setLoading(true)
    listLogs(logFilter(state, next))
      .then((p) => {
        if (g !== gen.current) return
        setEvents((ev) => [...ev, ...(p.events ?? [])])
        setNext(p.next_cursor)
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
  }, [next, loading, state])

  // infinite scroll: load the next page when the sentinel comes into view
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((entries) => entries.some((x) => x.isIntersecting) && loadMore())
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore])

  const addChip = (field: string, value: string) => {
    if (!value.trim()) return
    setState({ ...state, chips: { ...state.chips, [field]: value.trim() } })
  }
  const removeChip = (field: string) => {
    const chips = { ...state.chips }
    delete chips[field]
    setState({ ...state, chips })
  }
  const cols = columnsFor(state.kind)

  return (
    <div>
      <div role="tablist" className="mb-3 flex flex-wrap gap-1 border-b border-gray-200 dark:border-gray-700">
        {KINDS.map((k) => (
          <button
            key={k.kind || 'all'}
            role="tab"
            aria-selected={state.kind === k.kind}
            onClick={() => setState({ ...state, kind: k.kind })}
            className={`-mb-px border-b-2 px-3 py-1.5 text-sm ${state.kind === k.kind ? 'border-blue-600 text-blue-700 dark:text-blue-300' : 'border-transparent text-gray-600 hover:text-gray-900 dark:text-gray-400'}`}
          >
            {k.label}
          </button>
        ))}
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <form
          onSubmit={(e) => {
            e.preventDefault()
            setState({ ...state, q: q.trim() })
          }}
        >
          <input aria-label="Search" className={input + ' w-full sm:w-64'} placeholder="Search IP, domain, signature…" value={q} onChange={(e) => setQ(e.target.value)} />
        </form>
        <select aria-label="Time range" className={input} value={state.range} onChange={(e) => setState({ ...state, range: e.target.value as LogState['range'] })}>
          {RANGES.map((r) => <option key={r} value={r}>Last {r}</option>)}
        </select>
        <form
          className="flex items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault()
            addChip(chipField, chipValue)
            setChipValue('')
          }}
        >
          <select aria-label="Filter field" className={input} value={chipField} onChange={(e) => setChipField(e.target.value)}>
            {CHIP_FIELDS.map((f) => <option key={f} value={f}>{f}</option>)}
          </select>
          <span className="text-gray-500">=</span>
          <input aria-label="Filter value" className={input + ' w-36'} value={chipValue} onChange={(e) => setChipValue(e.target.value)} />
          <button type="submit" className="flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-sm dark:border-gray-600">
            <Filter size={14} /> Add filter
          </button>
        </form>
      </div>

      {Object.keys(state.chips).length > 0 && (
        <div className="mb-3 flex flex-wrap gap-2" data-testid="chips">
          {Object.entries(state.chips).map(([f, v]) => (
            <span key={f} className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs text-blue-800 dark:bg-blue-900 dark:text-blue-200">
              {f} = {v}
              <button type="button" aria-label={`Remove filter ${f}`} onClick={() => removeChip(f)}><X size={12} /></button>
            </span>
          ))}
        </div>
      )}

      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}

      <div className="overflow-x-auto rounded bg-white shadow-sm dark:bg-gray-800">
        <table className="min-w-full text-sm text-gray-700 dark:text-gray-200">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2">Time</th>
              {cols.map(([, h]) => <th key={h} className="px-3 py-2">{h}</th>)}
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.id} onClick={() => setDetail(e)} className="cursor-pointer border-t border-gray-100 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-700">
                <td className="whitespace-nowrap px-3 py-1">{fmtTime(e.time)}</td>
                {cols.map(([f, h]) => <td key={h} className="max-w-xs truncate px-3 py-1">{cell(e, f)}</td>)}
              </tr>
            ))}
            {!loading && events.length === 0 && !error && (
              <tr><td colSpan={cols.length + 1} className="px-3 py-6 text-center text-gray-500">No events match.</td></tr>
            )}
          </tbody>
        </table>
      </div>
      <div ref={sentinel} className="py-3 text-center text-sm text-gray-500">
        {loading ? 'Loading…' : next ? <button type="button" onClick={loadMore} className="text-blue-600 hover:underline">Load more</button> : events.length > 0 ? 'End of results' : null}
      </div>

      {detail && (
        <EditDrawer isOpen onClose={() => setDetail(null)} title={`${detail.kind} event`}>
          <dl className="grid grid-cols-1 sm:grid-cols-3 gap-x-2 gap-y-1 text-sm text-gray-700 dark:text-gray-300">
            <dt className="text-gray-500">time</dt>
            <dd className="sm:col-span-2">{fmtTime(detail.time)}</dd>
            {detail.src_ip && <CountryRow ip={detail.src_ip} />}
            {(Object.keys(detail) as (keyof LogEvent)[])
              .filter((k) => k !== 'time' && k !== 'id' && detail[k] !== undefined && detail[k] !== '')
              .map((k) => (
                <React.Fragment key={k}>
                  <dt className="text-gray-500">{k}</dt>
                  <dd className="sm:col-span-2 flex items-start justify-between gap-2 break-all">
                    <span>{String(detail[k])}</span>
                    {CHIP_FIELDS.includes(k) && (
                      <button
                        type="button"
                        title="Filter on this value"
                        aria-label={`Filter on ${k}`}
                        onClick={() => {
                          addChip(k, String(detail[k]))
                          setDetail(null)
                        }}
                        className="text-blue-600"
                      >
                        <Filter size={14} />
                      </button>
                    )}
                  </dd>
                </React.Fragment>
              ))}
          </dl>
          {detail.policy_id && /^\d+$/.test(detail.policy_id) && (
            <Link to={`/policy?edit=${detail.policy_id}`} className="mt-4 inline-flex min-h-[40px] items-center rounded bg-blue-600 px-3 py-1 text-sm text-white">
              Go to policy {detail.policy_id}
            </Link>
          )}
          {detail.action === 'deny' && detail.src_ip && detail.dst_ip && (
            <Link to={`/policy?${simulateQuery(detail)}`} className="mt-4 inline-flex min-h-[40px] items-center rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-100 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-700">
              Why was this denied?
            </Link>
          )}
        </EditDrawer>
      )}
    </div>
  )
}
