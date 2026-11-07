import React from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { t } from '../../../i18n'
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
        <p className="mt-4 text-sm text-gray-500">{t('sentry.notRunning')}</p>
      </div>
    )
  }

  if (q.isLoading) return <p className="text-sm text-gray-500">{t('common.loading')}</p>
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
        <ArrowLeft size={14} /> {t('sentry.allIncidents')}
      </Link>

      <div className="mt-3 rounded-lg border border-gray-200 bg-white p-5 dark:border-gray-700 dark:bg-gray-800">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-white">{incident.summary}</h2>
          <Badge variant={incident.status === 'pending' ? 'warning' : incident.status === 'acted' ? 'success' : 'secondary'}>
            {incident.status}
          </Badge>
          {incident.urgent && <Badge variant="error">urgent</Badge>}
        </div>

        <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
          <Field label={t('sentry.fSignature')}>{incident.alert.signature}</Field>
          <Field label={t('sentry.fSeverity')}>{incident.alert.severity === 0 ? 'unknown' : incident.alert.severity}</Field>
          <Field label={t('sentry.fDirection')}>{e.direction}</Field>
          <Field label={t('sentry.fSource')}>{incident.alert.src_ip}:{incident.alert.src_port ?? '—'}</Field>
          <Field label={t('sentry.fDestination')}>{incident.alert.dst_ip}:{incident.alert.dst_port ?? '—'}</Field>
          <Field label={t('sentry.fProtocol')}>{incident.alert.proto ?? '—'}</Field>
          {e.remote && <Field label={t('sentry.fRemoteContext')}>{e.remote}</Field>}
          {e.local_name && <Field label={t('sentry.fLocalHost')}>{e.local_name}</Field>}
          <Field label={t('sentry.fFirstSeen')}>{fmtTime(incident.first)}</Field>
          <Field label={t('sentry.fLastSeen')}>{fmtTime(incident.last)}</Field>
          <Field label={t('sentry.fTimesSeen')}>{incident.count}</Field>
        </dl>

        {incident.answer && (
          <div className="mt-4 rounded bg-gray-50 p-3 text-sm dark:bg-gray-700/40">
            {t('sentry.answeredWith')} <strong>{incident.answer}</strong>
            {incident.answered_by ? ` ${t('sentry.answeredBy', { user: incident.answered_by })}` : ''}.
          </div>
        )}

        <div className="mt-4">
          <h3 className="text-sm font-semibold text-gray-800 dark:text-gray-200">{t('sentry.recommendation')}</h3>
          <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">
            {incident.recommendation.tool}
            {incident.recommendation.target && ` ${incident.recommendation.target}`}
            {incident.recommendation.minutes != null &&
              ` ${t('sentry.forMinutesLong', { minutes: incident.recommendation.minutes })}`}
          </p>
        </div>
      </div>

      <h3 className="mt-6 text-base font-semibold text-gray-900 dark:text-white">
        {t('sentry.decisions')} ({decisions.length})
      </h3>
      {decisions.length === 0 && <p className="mt-2 text-sm text-gray-500">{t('sentry.noDecisions')}</p>}

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
        <Badge variant={d.executed ? 'success' : 'secondary'}>{d.executed ? t('sentry.executed') : t('sentry.notExecuted')}</Badge>
        <span className="text-xs text-gray-500">{fmtTime(d.time)}</span>
        <span className="text-xs text-gray-400">{t('sentry.modeLabel')}: {d.mode}</span>
      </div>

      <dl className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-2">
        <Field label={t('sentry.fDecidedBy')}>
          {d.by.startsWith('rule:') ? (
            <span title={d.rule_name}>{t('sentry.byRule')}{d.rule_name ? `: ${d.rule_name}` : ''}</span>
          ) : d.by.startsWith('model:') ? (
            `${t('sentry.byModel')} (${d.by.replace('model:', '')})`
          ) : d.by === 'default' ? (
            t('sentry.byDefault')
          ) : (
            `${t('sentry.byAdmin')} (${d.by.replace('admin:', '')})`
          )}
        </Field>
        <Field label={t('sentry.fAction')}>
          {d.action.tool}
          {d.action.target && ` ${d.action.target}`}
          {d.action.minutes != null && ` ${t('sentry.forMinutes', { minutes: d.action.minutes })}`}
        </Field>
        <Field label={t('sentry.fInputs')}>
          {d.inputs.direction} · {d.inputs.remote ?? d.inputs.local_name ?? '—'}
        </Field>
        <Field label={t('sentry.fResult')}>{d.result}</Field>
      </dl>

      {d.model && (
        <div className="mt-3 rounded bg-blue-50 p-3 text-sm dark:bg-blue-900/20">
          <p className="font-medium text-blue-800 dark:text-blue-200">
            {t('sentry.modelAdvice')} · {d.model.model}
          </p>
          <p className="mt-1 text-blue-700 dark:text-blue-300">
            {t('sentry.suggested')} <strong>{d.model.action.tool}</strong>
            {d.model.action.target && ` ${d.model.action.target}`}
            {d.model.action.minutes != null && ` ${t('sentry.forMinutes', { minutes: d.model.action.minutes })} `}
            {t('sentry.atConfidence')}{' '}
            {Math.round(d.model.confidence * 100)}% {t('sentry.confidenceDash')}{' '}
            {d.model.used ? t('sentry.adviceUsed') : d.model.act ? t('sentry.adviceActed') : t('sentry.adviceSuggested')}
          </p>
          <p className="mt-1 text-xs text-blue-600 dark:text-blue-400">{d.model.why}</p>
        </div>
      )}
    </div>
  )
}
