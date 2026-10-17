import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Shield, Check, X, Clock, Repeat } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { Dialog } from '../../../components/Dialog'
import SentryNav from './SentryNav'
import {
  getSentryStatus,
  getIncidents,
  answerIncident,
  isPluginNotRunning,
  type Incident,
  type Answer
} from '../../../api/sentry'

const severityLabel = (s: number) =>
  s === 1 ? 'High' : s === 2 ? 'Medium' : s === 3 ? 'Low' : s === 4 ? 'Info' : 'Unknown'

const severityVariant = (s: number): 'error' | 'warning' | 'secondary' | 'primary' =>
  s === 1 ? 'error' : s === 2 ? 'warning' : s <= 4 ? 'secondary' : 'primary'

export default function Queue() {
  const qc = useQueryClient()
  const [blockTarget, setBlockTarget] = useState<Incident | null>(null)
  const [alwaysTarget, setAlwaysTarget] = useState<Incident | null>(null)
  const [minutes, setMinutes] = useState(60)
  const [busy, setBusy] = useState(false)

  const statusQ = useQuery({ queryKey: ['sentry', 'status'], queryFn: getSentryStatus, retry: false })
  const incidentsQ = useQuery({
    queryKey: ['sentry', 'incidents', 'pending'],
    queryFn: () => getIncidents('pending'),
    retry: false,
    refetchInterval: 10000
  })

  const answerMut = useMutation({
    mutationFn: ({ id, answer, mins }: { id: number; answer: Answer; mins?: number }) =>
      mins != null ? answerIncident(id, answer, mins) : answerIncident(id, answer),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sentry'] })
    }
  })

  if (statusQ.error && isPluginNotRunning(statusQ.error)) {
    return (
      <div>
        <SentryNav />
        <div className="rounded-lg bg-yellow-50 p-4 text-sm text-yellow-800 dark:bg-yellow-900/20 dark:text-yellow-300">
          Goblin Sentry is not running. Enable it under{' '}
          <Link to="/system/plugins" className="underline">System → Plugins</Link>.
        </div>
      </div>
    )
  }

  const incidents = incidentsQ.data?.incidents ?? []

  const doAnswer = async (incident: Incident, answer: Answer, mins?: number) => {
    setBusy(true)
    try {
      await answerMut.mutateAsync({ id: incident.id, answer, mins })
      setBlockTarget(null)
      setAlwaysTarget(null)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <SentryNav />

      <h2 className="mt-6 mb-3 text-lg font-semibold text-gray-800 dark:text-white">
        Pending incidents{' '}
        {statusQ.data && (
          <span className="text-sm font-normal text-gray-500">
            ({statusQ.data.counts.pending ?? 0})
          </span>
        )}
      </h2>

      {incidentsQ.isLoading && <p className="text-sm text-gray-500">Loading…</p>}
      {incidentsQ.isError && (
        <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">
          {(incidentsQ.error as Error).message}
        </div>
      )}

      {incidents.length === 0 && !incidentsQ.isLoading && (
        <p className="text-sm text-gray-500">No incidents waiting for approval.</p>
      )}

      <div className="space-y-3">
        {incidents.map((inc) => (
          <div
            key={inc.id}
            className={`rounded-lg border p-4 ${
              inc.urgent
                ? 'border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-900/20'
                : 'border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800'
            }`}
          >
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0 flex-1">
                <div className="mb-1 flex flex-wrap items-center gap-2">
                  {inc.urgent && <Badge variant="error">urgent</Badge>}
                  <Badge variant={severityVariant(inc.alert.severity)}>
                    {severityLabel(inc.alert.severity)}
                  </Badge>
                  <span className="text-xs text-gray-500">{inc.alert.signature}</span>
                </div>
                <p className="text-sm text-gray-800 dark:text-gray-200">{inc.summary}</p>
                <p className="mt-1 text-xs text-gray-500">
                  {inc.enrichment.direction} · from {inc.alert.src_ip}:{inc.alert.src_port ?? '—'} →{' '}
                  {inc.alert.dst_ip}:{inc.alert.dst_port ?? '—'} · seen {inc.count}×
                </p>
                <p className="mt-1 text-xs text-gray-600 dark:text-gray-400">
                  Recommendation: <strong>{inc.recommendation.tool}</strong>
                  {inc.recommendation.target && ` ${inc.recommendation.target}`}
                  {inc.recommendation.minutes != null && ` for ${inc.recommendation.minutes} min`}
                </p>
              </div>
            </div>

            <div className="mt-3 flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => { setBlockTarget(inc); setMinutes(inc.recommendation.minutes ?? 60) }}
                className="flex items-center gap-1 rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700"
              >
                <Shield size={14} /> Block
              </button>
              <button
                type="button"
                onClick={() => doAnswer(inc, 'ignore')}
                disabled={busy}
                className="flex items-center gap-1 rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <X size={14} /> Ignore
              </button>
              <button
                type="button"
                onClick={() => doAnswer(inc, 'approve')}
                disabled={busy}
                className="flex items-center gap-1 rounded bg-green-600 px-3 py-1 text-sm text-white hover:bg-green-700"
              >
                <Check size={14} /> Approve
              </button>
              <button
                type="button"
                onClick={() => setAlwaysTarget(inc)}
                disabled={busy}
                className="flex items-center gap-1 rounded border border-blue-300 px-3 py-1 text-sm text-blue-700 hover:bg-blue-50 dark:border-blue-700 dark:text-blue-300 dark:hover:bg-blue-900/30"
              >
                <Repeat size={14} /> Always do this
              </button>
            </div>

            <Link to={`/plugins/sentry/incidents/${inc.id}`} className="mt-2 inline-block text-xs text-blue-600 hover:underline dark:text-blue-400">
              View details →
            </Link>
          </div>
        ))}
      </div>

      {/* Block confirm dialog */}
      <Dialog open={!!blockTarget} onClose={() => setBlockTarget(null)} title="Confirm block" size="small">
        {blockTarget && (
          <div className="space-y-4">
            <p className="text-sm text-gray-700 dark:text-gray-300">
              Block <strong>{blockTarget.alert.src_ip}</strong> for a temporary period?
            </p>
            <label className="block text-sm">
              Duration (minutes)
              <input
                type="number"
                min={1}
                max={10080}
                value={minutes}
                onChange={(e) => setMinutes(Number(e.target.value))}
                className="mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600"
              />
            </label>
            {answerMut.isError && (
              <p role="alert" className="text-sm text-red-600">{(answerMut.error as Error).message}</p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setBlockTarget(null)} className="rounded border border-gray-300 px-3 py-1 text-sm">
                Cancel
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => doAnswer(blockTarget, 'block', minutes)}
                className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50"
              >
                Block
              </button>
            </div>
          </div>
        )}
      </Dialog>

      {/* Always confirm dialog */}
      <Dialog open={!!alwaysTarget} onClose={() => setAlwaysTarget(null)} title="Always do this" size="small">
        {alwaysTarget && (
          <div className="space-y-4">
            <p className="text-sm text-gray-700 dark:text-gray-300">
              Create a rule that automatically{' '}
              <strong>{alwaysTarget.recommendation.tool}</strong>
              {alwaysTarget.recommendation.target && ` ${alwaysTarget.recommendation.target}`} for matching
              traffic in the future. This will be added to your rules list.
            </p>
            {answerMut.isError && (
              <p role="alert" className="text-sm text-red-600">{(answerMut.error as Error).message}</p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setAlwaysTarget(null)} className="rounded border border-gray-300 px-3 py-1 text-sm">
                Cancel
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => doAnswer(alwaysTarget, 'always')}
                className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
              >
                Create rule
              </button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}
