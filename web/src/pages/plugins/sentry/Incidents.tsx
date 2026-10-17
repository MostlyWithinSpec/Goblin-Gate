import React, { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Search } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import SentryNav from './SentryNav'
import ModeSelector from './ModeSelector'
import { getSentryStatus, getIncidents, isPluginNotRunning, type IncidentStatus } from '../../../api/sentry'

const STATUSES: (IncidentStatus | 'all')[] = ['all', 'pending', 'acted', 'recorded', 'ignored', 'failed']

const statusVariant = (s: IncidentStatus): 'primary' | 'secondary' | 'success' | 'warning' | 'error' =>
  s === 'pending' ? 'warning' : s === 'acted' ? 'success' : s === 'recorded' ? 'primary' : s === 'ignored' ? 'secondary' : 'error'

const fmtTime = (rfc3339: string) => new Date(rfc3339).toLocaleString()

export default function Incidents() {
  const [status, setStatus] = useState<IncidentStatus | 'all'>('all')
  const [search, setSearch] = useState('')

  const statusQ = useQuery({ queryKey: ['sentry', 'status'], queryFn: getSentryStatus, retry: false })
  const q = useQuery({
    queryKey: ['sentry', 'incidents', status],
    queryFn: () => getIncidents(status === 'all' ? undefined : status),
    retry: false
  })

  if (q.error && isPluginNotRunning(q.error)) {
    return (
      <div>
        <SentryNav />
        <p className="mt-4 text-sm text-gray-500">Goblin Sentry is not running.</p>
      </div>
    )
  }

  const incidents = (q.data?.incidents ?? []).filter((i) => {
    if (!search) return true
    const s = search.toLowerCase()
    return (
      i.summary.toLowerCase().includes(s) ||
      i.alert.signature.toLowerCase().includes(s) ||
      i.alert.src_ip.includes(s) ||
      i.alert.dst_ip.includes(s)
    )
  })

  return (
    <div>
      <SentryNav />
      <ModeSelector status={statusQ.data} />

      <div className="mt-4 flex flex-wrap items-center gap-3">
        <div className="flex gap-1 rounded-lg border border-gray-200 p-1 dark:border-gray-700">
          {STATUSES.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setStatus(s)}
              className={`rounded px-3 py-1 text-sm capitalize ${
                status === s
                  ? 'bg-blue-600 text-white'
                  : 'text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
              }`}
            >
              {s}
            </button>
          ))}
        </div>
        <div className="relative flex-1 min-w-[200px] max-w-xs">
          <Search size={14} className="absolute left-2 top-2 text-gray-400" />
          <input
            type="search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter by summary, signature, IP…"
            className="w-full rounded border border-gray-300 py-1 pl-7 pr-2 text-sm dark:bg-gray-700 dark:border-gray-600"
          />
        </div>
      </div>

      {q.isLoading && <p className="mt-4 text-sm text-gray-500">Loading…</p>}
      {q.isError && (
        <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
          {(q.error as Error).message}
        </div>
      )}

      {incidents.length === 0 && !q.isLoading && (
        <p className="mt-4 text-sm text-gray-500">No incidents found.</p>
      )}

      <div className="mt-4 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-800">
            <tr>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Status</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Time</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Summary</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Source</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Destination</th>
              <th className="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500">Count</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-700/50 dark:bg-gray-800">
            {incidents.map((inc) => (
              <tr key={inc.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/40">
                <td className="px-3 py-2">
                  <Badge variant={statusVariant(inc.status)}>{inc.status}</Badge>
                </td>
                <td className="whitespace-nowrap px-3 py-2 text-sm text-gray-500">{fmtTime(inc.first)}</td>
                <td className="max-w-xs truncate px-3 py-2 text-sm">
                  <Link to={`/plugins/sentry/incidents/${inc.id}`} className="text-blue-600 hover:underline dark:text-blue-400">
                    {inc.summary}
                  </Link>
                </td>
                <td className="whitespace-nowrap px-3 py-2 text-sm text-gray-500">{inc.alert.src_ip}</td>
                <td className="whitespace-nowrap px-3 py-2 text-sm text-gray-500">
                  {inc.alert.dst_ip}:{inc.alert.dst_port ?? '—'}
                </td>
                <td className="px-3 py-2 text-right text-sm text-gray-500">{inc.count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
