// Log & Report → Flow export (task 145): send the box's connections to a
// NetFlow v9 / IPFIX collector. The settings are the single candidate object at
// "flow_export"; the status line reads GET /api/v1/flow-export every 10 s and
// describes the running exporter, so a save that is not committed yet shows as
// "Commit to start".
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { inputClass } from '../../components/ConfigList'
import { t } from '../../i18n'
import { getCandidate, getFlowExportStatus, patchSection } from '../../api/objects'
import type { FlowExportSettings, FlowExportStatus } from '../../api/objects'

export interface FlowExportForm {
  enabled: boolean
  collector: string
  protocol: 'ipfix' | 'netflow9'
  timeout: string
}

const DEFAULT_TIMEOUT = 60

// formOf turns the stored section into the form. A protocol or a timeout that
// was never set gets its default, so the form never shows an empty select.
export function formOf(c: FlowExportSettings | undefined): FlowExportForm {
  return {
    enabled: c?.enabled ?? false,
    collector: c?.collector ?? '',
    protocol: c?.protocol === 'netflow9' ? 'netflow9' : 'ipfix',
    timeout: c?.active_timeout_seconds ? String(c.active_timeout_seconds) : String(DEFAULT_TIMEOUT),
  }
}

const HOST_PORT = /^[A-Za-z0-9_.-]+:\d{1,5}$/

// buildFlow turns the form into the candidate object. The defaults (IPFIX and a
// 60 s active timeout) are left out so the stored config stays small.
export function buildFlow(f: FlowExportForm): FlowExportSettings {
  const out: FlowExportSettings = { enabled: f.enabled }
  const collector = f.collector.trim()
  if (collector) out.collector = collector
  if (f.protocol === 'netflow9') out.protocol = 'netflow9'
  const timeout = Number(f.timeout.trim())
  if (f.timeout.trim() !== '' && Number.isFinite(timeout) && timeout !== DEFAULT_TIMEOUT) {
    out.active_timeout_seconds = Math.trunc(timeout)
  }
  return out
}

// validHostPort accepts a bare IPv6 in brackets and a name or IPv4 on its own
// with no port, which the server rejects too (config.validHostPort).
function validHostPort(v: string): boolean {
  if (v.startsWith('[')) return /^\[[0-9A-Fa-f:.]+\]:\d{1,5}$/.test(v)
  const at = v.lastIndexOf(':')
  if (at <= 0) return false
  if (v.slice(0, at).includes(':')) return false
  return HOST_PORT.test(v)
}

// validateFlow mirrors the server rules (config.checkFlowExport) so a bad save
// is shown next to its field instead of coming back as a validation list.
export function validateFlow(f: FlowExportForm): Record<string, string> {
  const e: Record<string, string> = {}
  const collector = f.collector.trim()
  if (f.enabled && !collector) e.collector = t('flowExport.errCollectorRequired')
  if (collector && !validHostPort(collector)) e.collector = t('flowExport.errCollectorFormat')
  const raw = f.timeout.trim()
  if (raw !== '') {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < 10 || n > 600) e.timeout = t('flowExport.errTimeout')
  }
  return e
}

// relTime renders an RFC3339 time as a short relative age.
function relTime(s?: string): string {
  if (!s) return t('flowExport.dash')
  const at = Date.parse(s)
  if (Number.isNaN(at)) return t('flowExport.dash')
  const sec = Math.max(0, (Date.now() - at) / 1000)
  if (sec < 60) return t('logexport.justNow')
  if (sec < 3600) return t('devices.minutesAgo', { count: Math.floor(sec / 60) })
  if (sec < 86400) return t('devices.hoursAgo', { count: Math.floor(sec / 3600) })
  return t('devices.daysAgo', { count: Math.floor(sec / 86400) })
}

export default function FlowExportPage() {
  const cfg = useQuery({ queryKey: ['candidate'], queryFn: getCandidate })
  // 404 when the exporter never started, so not finding it is a state to show,
  // not a failure to report.
  const status = useQuery({
    queryKey: ['flow-export-status'],
    queryFn: getFlowExportStatus,
    refetchInterval: 10_000,
    retry: false,
  })

  const [form, setForm] = useState<FlowExportForm | null>(null)
  const [saved, setSaved] = useState<FlowExportSettings | null>(null)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null)

  useEffect(() => {
    if (!form && cfg.data) setForm(formOf(cfg.data.flow_export))
  }, [form, cfg.data])

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!form) return
    const errs = validateFlow(form)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    const body = buildFlow(form)
    try {
      const res = await patchSection('flow_export', body)
      const server: Record<string, string> = {}
      // A server error names its field as flow_export.<field>; keep the first
      // message per field so it lands under the right input.
      const fieldName = (p: string) => {
        const f = p.startsWith('flow_export.') ? p.slice('flow_export.'.length) : p
        return f === 'active_timeout_seconds' ? 'timeout' : f
      }
      for (const e of res.validation_errors ?? []) {
        const key = fieldName(e.path)
        if (!server[key]) server[key] = e.message
      }
      setErrors(server)
      if (Object.keys(server).length > 0) {
        setMessage(null)
        return
      }
      setSaved(body)
      setMessage({ ok: true, text: t('system.savedCommit') })
    } catch (e) {
      setErrors({})
      setMessage({ ok: false, text: t('crowdSec.saveFailed', { error: (e as Error).message }) })
    }
  }

  const set = (patch: Partial<FlowExportForm>) => {
    setMessage(null)
    setForm((f) => (f ? { ...f, ...patch } : f))
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">{t('flowExport.title')}</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400">{t('flowExport.intro')}</p>
      </div>

      <StatusCard query={status} saved={saved} />

      <section className="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">{t('flowExport.setup')}</h2>
        {!form ? (
          <p className="text-sm text-gray-500">{t('common.loading')}</p>
        ) : (
          <form onSubmit={save} className="max-w-xl space-y-3">
            {message && (
              <div
                role={message.ok ? 'status' : 'alert'}
                className={
                  'rounded p-2 text-sm ' +
                  (message.ok
                    ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300'
                    : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300')
                }
              >
                {message.text}
              </div>
            )}
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                aria-label={t('flowExport.enableAria')}
                checked={form.enabled}
                onChange={(e) => set({ enabled: e.target.checked })}
              />
              {t('flowExport.enable')}
            </label>

            <label className="block text-sm">
              {t('flowExport.collector')}
              <input
                className={inputClass}
                aria-label={t('flowExport.collector')}
                placeholder="10.0.0.5:2055"
                value={form.collector}
                onChange={(e) => set({ collector: e.target.value })}
              />
            </label>
            {errors.collector && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.collector}</p>}

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="block text-sm">
                {t('flowExport.protocol')}
                <select
                  className={inputClass}
                  aria-label={t('flowExport.protocol')}
                  value={form.protocol}
                  onChange={(e) => set({ protocol: e.target.value === 'netflow9' ? 'netflow9' : 'ipfix' })}
                >
                  <option value="ipfix">{t('flowExport.protocolIpfix')}</option>
                  <option value="netflow9">{t('flowExport.protocolNetflow9')}</option>
                </select>
              </label>
              <label className="block text-sm">
                {t('flowExport.timeout')}
                <input
                  type="number"
                  className={inputClass}
                  aria-label={t('flowExport.timeout')}
                  placeholder={String(DEFAULT_TIMEOUT)}
                  value={form.timeout}
                  onChange={(e) => set({ timeout: e.target.value })}
                />
              </label>
            </div>
            {errors.protocol && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.protocol}</p>}
            {errors.timeout && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.timeout}</p>}

            <Button type="submit">{t('flowExport.save')}</Button>
          </form>
        )}
      </section>

      <HelpPanel />
    </div>
  )
}

// StatusState is the plain query state so the card can also render the
// "endpoint unavailable" case (the exporter is not running at all).
interface StatusState {
  data?: FlowExportStatus
  isError: boolean
  error: unknown
}

// saved is the section as last PATCHed to the candidate. The status comes from
// the running exporter, so what was saved but no commit has applied shows as
// "Commit to start" — and an endpoint that cannot be read at all (the exporter
// never started) counts as not running for that comparison.
export function StatusCard({ query, saved }: { query: StatusState; saved: FlowExportSettings | null }) {
  const s = query.data
  const err = query.error
  const pending = !!saved?.enabled && saved.enabled !== (query.isError ? false : s?.enabled)
  return (
    <section data-testid="flow-export-status" className="space-y-2 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">{t('flowExport.status')}</h2>
        {query.isError ? (
          <Badge variant="error">{t('flowExport.notRunning')}</Badge>
        ) : pending ? (
          <Badge variant="warning">{t('flowExport.commitToStart')}</Badge>
        ) : s?.running ? (
          <Badge variant="success">{t('flowExport.running')}</Badge>
        ) : (
          <Badge variant={s?.enabled ? 'warning' : 'secondary'}>{t('flowExport.notRunning')}</Badge>
        )}
      </div>
      {query.isError ? (
        <p role="alert" data-testid="flow-export-error" className="text-sm text-red-600 dark:text-red-400">
          {t('flowExport.statusUnavailable')} {err instanceof Error ? err.message : t('flowExport.notRunning')}
        </p>
      ) : !s ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : (
        <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4" data-testid="flow-export-counters">
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.collectorLabel')}</dt>
            <dd className="text-gray-900 dark:text-white">{s.collector || t('flowExport.dash')}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.protocolLabel')}</dt>
            <dd className="text-gray-900 dark:text-white">{s.protocol === 'netflow9' ? 'NetFlow v9' : 'IPFIX'}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.recordsSent')}</dt>
            <dd className="font-semibold text-gray-900 dark:text-white">{s.records_sent.toLocaleString()}</dd>
          </div>
          {(s.records_dropped ?? 0) > 0 && (
            <div>
              <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.recordsDropped')}</dt>
              <dd className="font-semibold text-red-700 dark:text-red-300">{(s.records_dropped ?? 0).toLocaleString()}</dd>
            </div>
          )}
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.packetsSent')}</dt>
            <dd className="font-semibold text-gray-900 dark:text-white">{s.packets_sent.toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.lastSent')}</dt>
            <dd className="text-gray-900 dark:text-white">{relTime(s.last_sent)}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">{t('flowExport.openFlows')}</dt>
            <dd className="text-gray-900 dark:text-white">{s.open_flows}</dd>
          </div>
        </dl>
      )}
      {s?.last_error && (
        <p role="alert" data-testid="flow-export-last-error" className="text-sm text-red-600 dark:text-red-400">
          {t('crowdSec.lastError', { error: s.last_error })}
        </p>
      )}
    </section>
  )
}

function HelpPanel() {
  return (
    <section className="rounded-lg border border-gray-200 p-4 text-sm dark:border-gray-700">
      <h2 className="text-base font-semibold text-gray-800 dark:text-white">{t('flowExport.helpTitle')}</h2>
      <p className="mt-1 text-gray-600 dark:text-gray-300">{t('flowExport.helpSampled')}</p>
      <p className="mt-2 text-gray-600 dark:text-gray-300">{t('flowExport.helpCollectors')}</p>
    </section>
  )
}
