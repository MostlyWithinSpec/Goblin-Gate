import React from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import SentryNav from './SentryNav'
import { getIncident, isPluginNotRunning, type Decision } from '../../../api/sentry'

const fmtTime = (rfc3339: string) => new Date(rfc3339).toLocaleString()

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase text-gray-500">{label}</dt>
      <dd className="mt-0.5 text-sm text-gray-800 dark:text-gray-200">{children}</dd>
    </div>
  )
}

export default function IncidentDetail() {
  const { id } = useParams<{ id: string }>()
  const q = useQuery({
    queryKey: ['sentry', 'incident', id],
    queryFn: () => getIncident(Number(id)),
    enabled: id != null,
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

  if (q.isLoading) return <p className="text-sm text-gray-500">Loading…</p>
  if (q.error || !q.data) {
    return (
      <div>
        <SentryNav />
        <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
          {(q.error as Error).message}
        </div>
      </div>
    )
  }

  const { incident, decisions } = q.data
  const e = incident.enrichment

  return (
    <div>
      <SentryNav />

      <Link to="/plugins/sentry/incidents" className="mt-4 inline-flex items-center gap-1 text-sm text-blue-600 hover:underline dark:text-blue-400">
        <ArrowLeft size={14} /> All incidents
      </Link>

      <div className="mt-3 rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-800">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">{incident.summary}</h2>
          <Badge variant={incident.status === 'pending' ? 'warning' : incident.status === 'acted' ? 'success' : 'secondary'}>
            {incident.status}
          </Badge>
          {incident.urgent && <Badge variant="error">urgent</Badge>}
        </div>

        <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2 lg:grid-cols-1 sm:grid-cols-3">
          <Field label="Signature">{incident.alert.signature}</Field>
          <Field label="Severity">{incident.alert.severity === 0 ? 'unknown' : incident.alert.severity}</Field>
          <Field label="Direction">{e.direction}</Field>
          <Field label="Source">{incident.alert.src_ip}:{incident.alert.src_port ?? '—'}</Field>
          <Field label="Destination">{incident.alert.dst_ip}:{incident.alert.dst_port ?? '—'}</Field>
          <Field label="Protocol">{incident.alert.proto ?? '—'}</Field>
          {e.remote && <Field label="Remote context">{e.remote}</Field>}
          {e.local_name && <Field label="Local host">{e.local_name}</Field>}
          <Field label="First seen">{fmtTime(incident.first)}</Field>
          <Field label="Last seen">{fmtTime(incident.last)}</Field>
          <Field label="Times seen">{incident.count}</Field>
        </dl>

        {incident.answer && (
          <div className="mt-4 rounded bg-gray-50 p-3 text-sm dark:bg-gray-700/40">
            Answered with <strong>{incident.answer}</strong>
            {incident.answered_by ? ` by ${incident.answered_by}` : ''}.
          </div>
        )}

        <div className="mt-4">
          <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200">Recommendation</h3>
          <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">
            {incident.recommendation.tool}
            {incident.recommendation.target && ` ${incident.recommendation.target}`}
            {incident.recommendation.minutes != null && ` for ${incident.recommendation.minutes} minutes`}
          </p>
        </div>
      </div>

      <h3 className="mt-6 text-base font-semibold text-gray-900 dark:text-white">
        Decisions ({decisions.length})
      </h3>
      {decisions.length === 0 && <p className="mt-2 text-sm text-gray-500">No decisions recorded yet.</p>}

      <div className="mt-2 space-y-3">
        {decisions.map((d) => (
          <DecisionCard key={d.id} d={d} />
        ))}
      </div>
    </div>
  )
}

function DecisionCard({ d }: { d: Decision }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Badge variant={d.executed ? 'success' : 'secondary'}>{d.executed ? 'executed' : 'not executed'}</Badge>
        <span className="text-xs text-gray-500">{fmtTime(d.time)}</span>
        <span className="text-xs text-gray-400">mode: {d.mode}</span>
      </div>

      <dl className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-2">
        <Field label="Decided by">
          {d.by.startsWith('rule:') ? (
            <span title={d.rule_name}>Rule{d.rule_name ? `: ${d.rule_name}` : ''}</span>
          ) : d.by.startsWith('model:') ? (
            `Model (${d.by.replace('model:', '')})`
          ) : d.by === 'default' ? (
            'Default policy'
          ) : (
            `Admin (${d.by.replace('admin:', '')})`
          )}
        </Field>
        <Field label="Action">
          {d.action.tool}
          {d.action.target && ` ${d.action.target}`}
          {d.action.minutes != null && ` for ${d.action.minutes} min`}
        </Field>
        <Field label="Inputs (enrichment)">
          {d.inputs.direction} · {d.inputs.remote ?? d.inputs.local_name ?? '—'}
        </Field>
        <Field label="Result">{d.result}</Field>
      </dl>

      {d.model && (
        <div className="mt-3 rounded bg-blue-50 p-3 text-sm dark:bg-blue-900/20">
          <p className="font-medium text-blue-800 dark:text-blue-200">
            Model advice · {d.model.model}
          </p>
          <p className="mt-1 text-blue-700 dark:text-blue-300">
            Suggested <strong>{d.model.action.tool}</strong>
            {d.model.action.target && ` ${d.model.action.target}`}
            {d.model.action.minutes != null && ` for ${d.model.action.minutes} min`} at{' '}
            {Math.round(d.model.confidence * 100)}% confidence —{' '}
            {d.model.used ? 'used' : d.model.act ? 'acted on' : 'suggested only'}
          </p>
          <p className="mt-1 text-xs text-blue-600 dark:text-blue-400">{d.model.why}</p>
        </div>
      )}
    </div>
  )
}
