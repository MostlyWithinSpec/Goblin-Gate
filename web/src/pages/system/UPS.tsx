// System → UPS (task 113): point the box at a UPS on its own USB port or at a
// NUT server elsewhere, choose when to shut down, and watch the battery. The
// settings are the single candidate object at "ups"; the status card reads
// GET /api/v1/ups every 15 s.
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { inputClass } from '../../components/ConfigList'
import { getCandidate, getUPSStatus, patchSection } from '../../api/objects'
import type { UPSSettings, UPSStatus } from '../../api/objects'

const DEFAULT_PORT = 3493

// A stored password is either the real one (a caller that may see secrets got
// it back), hidden (the server blanked it, so it must be sent back empty and
// the server puts the stored value back), or there is none.
type StoredSecret = 'real' | 'hidden' | 'none'

function storedSecretOf(settings: UPSSettings | undefined): StoredSecret {
  if (!settings) return 'none'
  if (settings.password) return 'real'
  // A password only ever comes back blanked next to a full remote target, so
  // a blank one without a host really means "nothing stored".
  return settings.mode === 'remote' && settings.host ? 'hidden' : 'none'
}

export interface UPSForm {
  enabled: boolean
  mode: 'usb' | 'remote'
  host: string
  port: string
  name: string
  username: string
  password: string
  percent: string
  runtime: string
  // hasSecret is the form's own memory of whether the server holds a password:
  // it starts from the loaded config and turns true once one is typed and
  // saved, so a later save that only changes a threshold does not wipe it.
  hasSecret: boolean
}

// buildUps turns the form into the candidate object. Remote-only fields are
// dropped for a USB UPS, and a password that is not retyped is either sent
// back exactly as it was loaded (a caller that may see secrets holds the real
// one) or left out so the server keeps the stored value.
export function buildUps(f: UPSForm, loaded: UPSSettings | undefined): UPSSettings {
  const out: UPSSettings = { enabled: f.enabled, mode: f.mode }
  if (f.mode === 'remote') {
    out.host = f.host.trim()
    const port = Number(f.port.trim())
    out.port = Number.isFinite(port) && port > 0 ? Math.trunc(port) : DEFAULT_PORT
    out.name = f.name.trim()
    out.username = f.username.trim()
    const typed = f.password.trim()
    if (typed) out.password = typed
    else if (storedSecretOf(loaded) === 'real') out.password = loaded?.password ?? ''
  }
  const percent = Number(f.percent.trim())
  if (f.percent.trim() !== '' && Number.isFinite(percent)) out.shutdown_percent = Math.trunc(percent)
  const runtime = Number(f.runtime.trim())
  if (f.runtime.trim() !== '' && Number.isFinite(runtime)) out.shutdown_runtime = Math.trunc(runtime)
  return out
}

// validateUps mirrors the server rules (config.checkUPS) so a bad save is shown
// in the form instead of coming back as a validation_errors list.
export function validateUps(f: UPSForm): Record<string, string> {
  const e: Record<string, string> = {}
  if (f.enabled && f.mode === 'remote') {
    if (!f.host.trim()) e.host = 'Required for a remote NUT server.'
    if (!f.name.trim()) e.name = 'Required: the UPS name on the NUT server (upsc -l lists them).'
    if (!f.username.trim()) e.username = 'Required: the NUT user allowed to monitor the UPS.'
    if (!f.password.trim() && !f.hasSecret) e.password = 'Required for a remote NUT server.'
  }
  const check = (raw: string, lo: number, hi: number, unit: string): string | undefined => {
    const t = raw.trim()
    if (t === '' || t === '0') return undefined
    const n = Number(t)
    if (!Number.isInteger(n) || n < lo || n > hi) return `Want ${lo}-${hi} ${unit}, or 0 / empty = when the UPS says the battery is low.`
    return undefined
  }
  const port = Number(f.port.trim())
  if (f.port.trim() !== '' && (!Number.isInteger(port) || port < 1 || port > 65535)) {
    e.port = 'Want 1-65535, or leave it empty for 3493.'
  }
  const percent = check(f.percent, 5, 90, '%')
  if (percent) e.percent = percent
  const runtime = check(f.runtime, 60, 3600, 'seconds')
  if (runtime) e.runtime = runtime
  return e
}
// relTime renders an RFC3339 time as a short relative age.
function relTime(s?: string): string {
  if (!s) return '—'
  const t = Date.parse(s)
  if (Number.isNaN(t)) return '—'
  const sec = Math.max(0, (Date.now() - t) / 1000)
  if (sec < 60) return 'just now'
  if (sec < 3600) return `${Math.floor(sec / 60)} min ago`
  if (sec < 86400) return `${Math.floor(sec / 3600)} h ago`
  return `${Math.floor(sec / 86400)} d ago`
}

export function formOf(c: UPSSettings | undefined): UPSForm {
  return {
    enabled: c?.enabled ?? false,
    mode: c?.mode === 'remote' ? 'remote' : 'usb',
    host: c?.host ?? '',
    port: c?.port ? String(c.port) : String(DEFAULT_PORT),
    name: c?.name ?? '',
    username: c?.username ?? '',
    password: '',
    percent: c?.shutdown_percent ? String(c.shutdown_percent) : '',
    runtime: c?.shutdown_runtime ? String(c.shutdown_runtime) : '',
    hasSecret: storedSecretOf(c) !== 'none',
  }
}

export default function UPS() {
  const queryClient = useQueryClient()
  const cfg = useQuery({ queryKey: ['candidate'], queryFn: getCandidate })
  const status = useQuery({ queryKey: ['ups-status'], queryFn: getUPSStatus, refetchInterval: 15_000 })

  const [form, setForm] = useState<UPSForm | null>(null)
  const [loaded, setLoaded] = useState<UPSSettings | undefined>(undefined)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!form && cfg.data) {
      setLoaded(cfg.data.ups)
      setForm(formOf(cfg.data.ups))
    }
  }, [form, cfg.data])

  const save = useMutation({
    mutationFn: (value: UPSSettings) => patchSection('ups', value),
    onSuccess: (res, value) => {
      const errs: Record<string, string> = {}
      const field = (p: string) => (p.startsWith('ups.') ? p.slice(4) : p)
      const other = (k: string) => (k === 'shutdown_percent' ? 'percent' : k === 'shutdown_runtime' ? 'runtime' : k)
      for (const e of res.validation_errors ?? []) {
        const key = other(field(e.path))
        if (!errs[key]) errs[key] = e.message
      }
      setErrors(errs)
      if (Object.keys(errs).length > 0) {
        setMessage('')
        return
      }
      setForm((f) => (f ? { ...f, password: '', hasSecret: f.hasSecret || value.password !== undefined } : f))
      // Remember what the server holds now: a password that was sent is the
      // password; one left out stayed as it was.
      setLoaded((c) => ({ ...c, ...value, password: value.password ?? c?.password }))
      setMessage('Saved.')
      queryClient.invalidateQueries({ queryKey: ['candidate'] })
      queryClient.invalidateQueries({ queryKey: ['ups-status'] })
    },
    onError: (err: Error) => {
      setErrors({})
      setMessage(`Could not save: ${err.message}`)
    },
  })

  const set = (patch: Partial<UPSForm>) => {
    setMessage('')
    setForm((f) => (f ? { ...f, ...patch } : f))
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!form) return
    const errs = validateUps(form)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    save.mutate(buildUps(form, loaded))
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">UPS</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400">
          Watch a UPS on this box&rsquo;s USB port or one a NUT server elsewhere already monitors, and shut
          down cleanly before the battery runs out.
        </p>
      </div>

      <StatusCard query={status} />

      <section className="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">Setup</h2>
        {!form ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : (
          <form onSubmit={submit} className="max-w-xl space-y-3">
            {message && (
              <div role="status" className="rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/20 dark:text-green-300">
                {message}
              </div>
            )}
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" aria-label="Enable UPS monitoring" checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
              Monitor the UPS
            </label>

            <label className="block text-sm">
              Where is the UPS?
              <select
                className={inputClass}
                aria-label="UPS mode"
                value={form.mode}
                onChange={(e) => set({ mode: e.target.value === 'remote' ? 'remote' : 'usb' })}
              >
                <option value="usb">On this box (USB)</option>
                <option value="remote">On a remote NUT server</option>
              </select>
            </label>
            {errors.mode && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.mode}</p>}

            {form.mode === 'remote' && (
              <div className="space-y-3">
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label className="block text-sm">
                    NUT server
                    <input
                      className={inputClass}
                      aria-label="NUT server"
                      placeholder="192.168.1.10"
                      value={form.host}
                      onChange={(e) => set({ host: e.target.value })}
                    />
                  </label>
                  {errors.host && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.host}</p>}
                  <label className="block text-sm">
                    Port
                    <input
                      type="number"
                      className={inputClass}
                      aria-label="NUT port"
                      placeholder={String(DEFAULT_PORT)}
                      value={form.port}
                      onChange={(e) => set({ port: e.target.value })}
                    />
                  </label>
                  {errors.port && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.port}</p>}
                  <label className="block text-sm">
                    UPS name
                    <input
                      className={inputClass}
                      aria-label="UPS name"
                      placeholder="ups"
                      value={form.name}
                      onChange={(e) => set({ name: e.target.value })}
                    />
                  </label>
                  {errors.name && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.name}</p>}
                  <label className="block text-sm">
                    NUT user
                    <input
                      className={inputClass}
                      aria-label="NUT user"
                      placeholder="monitor"
                      value={form.username}
                      onChange={(e) => set({ username: e.target.value })}
                    />
                  </label>
                  {errors.username && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.username}</p>}
                </div>
                <label className="block text-sm">
                  NUT password
                  <input
                    type="password"
                    className={inputClass}
                    aria-label="NUT password"
                    placeholder={form.hasSecret ? 'unchanged' : ''}
                    value={form.password}
                    onChange={(e) => set({ password: e.target.value })}
                  />
                </label>
                {errors.password && (
                  <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.password}</p>
                )}
              </div>
            )}

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <label className="block text-sm">
                Shut down at charge (%)
                <input
                  type="number"
                  className={inputClass}
                  aria-label="Shut down at charge"
                  placeholder="0 = when the UPS says the battery is low"
                  value={form.percent}
                  onChange={(e) => set({ percent: e.target.value })}
                />
              </label>
              <label className="block text-sm">
                Shut down at runtime left (seconds)
                <input
                  type="number"
                  className={inputClass}
                  aria-label="Shut down at runtime"
                  placeholder="0 = when the UPS says the battery is low"
                  value={form.runtime}
                  onChange={(e) => set({ runtime: e.target.value })}
                />
              </label>
            </div>
            {errors.percent && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.percent}</p>}
            {errors.runtime && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.runtime}</p>}

            <Button type="submit" disabled={save.isPending}>
              Save
            </Button>
          </form>
        )}
      </section>

      <HelpPanel />
    </div>
  )
}

// stateBadge is the one place a UPS state becomes a badge, so the page and the
// dashboard widget say the same thing.
export function StateBadge({ state, connected, enabled }: { state: string; connected: boolean; enabled: boolean }) {
  if (!enabled) return <Badge variant="secondary">Disabled</Badge>
  if (!connected) return <Badge variant="secondary">Not connected</Badge>
  switch (state) {
    case 'online':
      return <Badge variant="success">On mains</Badge>
    case 'on_battery':
      return <Badge variant="warning">On battery</Badge>
    case 'low_battery':
      return <Badge variant="error">Low battery</Badge>
    default:
      return <Badge variant="secondary">Unknown</Badge>
  }
}

// ChargeBar draws the battery level; the colour follows the state so a low
// battery reads red even before the badge does.
export function ChargeBar({ charge, state }: { charge?: number; state: string }) {
  const pct = typeof charge === 'number' ? Math.max(0, Math.min(100, charge)) : null
  const colour =
    state === 'low_battery' ? 'bg-red-500' : state === 'on_battery' ? 'bg-amber-500' : 'bg-green-500'
  return (
    <div className="w-full" data-testid="ups-charge">
      <div className="h-2 w-full overflow-hidden rounded bg-gray-200 dark:bg-gray-700">
        {pct !== null && (
          <div
            className={`h-full ${colour}`}
            style={{ width: `${pct}%` }}
            role="progressbar"
            aria-label="Battery charge"
            aria-valuenow={Math.round(pct)}
            aria-valuemin={0}
            aria-valuemax={100}
          />
        )}
      </div>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {pct === null ? 'Charge unknown' : `Charge ${Math.round(pct)} %`}
      </p>
    </div>
  )
}

// runtimeText renders battery runtime left as minutes.
export function runtimeText(seconds?: number): string {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) return '—'
  const min = Math.round(seconds / 60)
  if (min < 1) return 'less than a minute'
  if (min < 120) return `about ${min} min`
  return `about ${Math.floor(min / 60)} h ${min % 60} min`
}

// StatusState is the plain query state, so the card can also render the
// "endpoint unavailable" case.
interface StatusState {
  data?: UPSStatus
  isError: boolean
  error: unknown
}

export function StatusCard({ query }: { query: StatusState }) {
  const s = query.data
  const err = query.error
  return (
    <section data-testid="ups-status" className="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">Status</h2>
        {query.isError ? (
          <Badge variant="error">Unavailable</Badge>
        ) : s ? (
          <StateBadge state={s.state} connected={s.connected} enabled={s.enabled} />
        ) : null}
      </div>
      {query.isError ? (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          Cannot read the UPS status: {err instanceof Error ? err.message : 'unavailable'}
        </p>
      ) : !s ? (
        <p className="text-sm text-gray-500">Loading…</p>
      ) : (
        <>
          <div className="max-w-sm">
            <ChargeBar charge={s.charge} state={s.state} />
          </div>
          <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-gray-500 dark:text-gray-400">Runtime left</dt>
              <dd className="font-semibold text-gray-900 dark:text-white" data-testid="ups-runtime">
                {runtimeText(s.runtime_seconds)}
              </dd>
            </div>
            <div>
              <dt className="text-gray-500 dark:text-gray-400">Load</dt>
              <dd className="text-gray-900 dark:text-white">{typeof s.load === 'number' ? `${Math.round(s.load)} %` : '—'}</dd>
            </div>
            <div>
              <dt className="text-gray-500 dark:text-gray-400">Model</dt>
              <dd className="text-gray-900 dark:text-white">{s.model || '—'}</dd>
            </div>
            <div>
              <dt className="text-gray-500 dark:text-gray-400">Last reading</dt>
              <dd className="text-gray-900 dark:text-white">{relTime(s.last_update)}</dd>
            </div>
            {s.last_event && (
              <div>
                <dt className="text-gray-500 dark:text-gray-400">Last event</dt>
                <dd className="text-gray-900 dark:text-white">
                  {s.last_event.kind.replace(/^ups_/, ' ').replace(/_/g, ' ').trim() || s.last_event.kind} · {relTime(s.last_event.time)}
                </dd>
              </div>
            )}
            {s.flags && s.flags.length > 0 && (
              <div>
                <dt className="text-gray-500 dark:text-gray-400">UPS flags</dt>
                <dd className="text-gray-900 dark:text-white">{s.flags.join(' ')}</dd>
              </div>
            )}
          </dl>
        </>
      )}
      {s?.last_error && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          Last error: {s.last_error}
        </p>
      )}
    </section>
  )
}

function HelpPanel() {
  return (
    <section className="rounded-lg border border-gray-200 p-4 text-sm dark:border-gray-700">
      <h2 className="text-base font-semibold text-gray-800 dark:text-white">How it works</h2>
      <p className="mt-1 text-gray-600 dark:text-gray-300">
        With <strong>USB</strong> the box runs the NUT driver and server itself — plug the UPS in and enable
        it. With a <strong>remote NUT server</strong> the box only monitors what that server already watches,
        so add a NUT user there that may monitor the UPS, then give its name and password above.
      </p>
      <p className="mt-2 text-gray-600 dark:text-gray-300">
        On battery the box raises an alert. When the charge or the runtime left drops to your threshold it
        shuts down cleanly; leave both at 0 and the UPS&rsquo;s own low-battery signal decides. See{' '}
        <span className="font-mono text-xs">UPS (power failures)</span> in the Updates and Backups wiki page.
      </p>
    </section>
  )
}
