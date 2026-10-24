// Security → CrowdSec (task 105): point the built-in bouncer at a CrowdSec
// LAPI and watch what it drops. The settings are the single candidate object
// at "crowdsec"; the status card reads GET /api/v1/crowdsec every 15 s.
import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge } from '../../components/Badge'
import { Button } from '../../components/Button'
import { inputClass } from '../../components/ConfigList'
import { getCandidate, getCrowdSecStatus, patchSection } from '../../api/objects'
import type { CrowdSecSettings, CrowdSecStatus } from '../../api/objects'

const WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Threat-Feeds-and-DNS-Filter#crowdsec'
const DEFAULT_POLL = 10

// A stored key is either the real one (a caller that may see secrets got it
// back), hidden (the server blanked it, so it must be sent back empty and the
// server puts the stored value back), or there is none.
type StoredKey = 'real' | 'hidden' | 'none'

function storedKeyOf(settings: CrowdSecSettings | undefined): StoredKey {
  if (!settings) return 'none'
  if (settings.api_key) return 'real'
  // With no URL there cannot be a usable key either, so an empty key really
  // means "nothing stored" — a key only ever comes back blanked next to a URL.
  return settings.url ? 'hidden' : 'none'
}

export interface CrowdSecForm {
  enabled: boolean
  url: string
  api_key: string
  poll: string
  scope: 'wan' | 'all'
  // hasKey is the form's own memory of whether the server holds a key: it
  // starts from the loaded config and turns true once a key is typed and
  // saved, so a later save that only flips "enabled" off does not wipe it.
  hasKey: boolean
}

// buildCrowdSec turns the form into the candidate object. A key that was
// already stored and is not retyped is sent back exactly as it was loaded
// (a caller that may see secrets holds the real key); when the key is hidden
// the field is left out so the server keeps the stored value.
export function buildCrowdSec(f: CrowdSecForm, loaded: CrowdSecSettings | undefined): CrowdSecSettings {
  const out: CrowdSecSettings = {
    enabled: f.enabled,
    url: f.url.trim(),
    scope: f.scope,
  }
  const poll = Number(f.poll)
  out.poll_seconds = Number.isFinite(poll) && poll > 0 ? Math.trunc(poll) : DEFAULT_POLL
  const typed = f.api_key.trim()
  if (typed) out.api_key = typed
  else if (storedKeyOf(loaded) === 'real') out.api_key = loaded?.api_key ?? ''
  return out
}

// validateCrowdSec mirrors the server rules so a bad save is shown in the
// form instead of coming back as a validation_errors list.
export function validateCrowdSec(f: CrowdSecForm): Record<string, string> {
  const e: Record<string, string> = {}
  const url = f.url.trim()
  if (f.enabled && !url) e.url = 'Required when CrowdSec is enabled.'
  if (url) {
    let parsed: URL | null = null
    try {
      parsed = new URL(url)
    } catch {
      parsed = null
    }
    if (!parsed || !parsed.hostname || (parsed.protocol !== 'http:' && parsed.protocol !== 'https:')) {
      e.url = 'Give the full LAPI base URL, e.g. http://192.168.1.20:8080.'
    } else if (parsed.username || parsed.password) {
      e.url = 'No credentials in the URL: put the bouncer key below.'
    }
  }
  if (f.enabled && !f.api_key.trim() && !f.hasKey) {
    e.api_key = 'Required when CrowdSec is enabled (cscli bouncers add goblin-gate).'
  }
  const poll = Number(f.poll)
  if (f.poll.trim() !== '' && (!Number.isInteger(poll) || poll < 5 || poll > 300)) {
    e.poll = 'Choose 5-300 seconds.'
  }
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

export default function CrowdSec() {
  const queryClient = useQueryClient()
  const cfg = useQuery({ queryKey: ['candidate'], queryFn: getCandidate })
  const status = useQuery({ queryKey: ['crowdsec-status'], queryFn: getCrowdSecStatus, refetchInterval: 15_000 })

  const [form, setForm] = useState<CrowdSecForm | null>(null)
  const [loaded, setLoaded] = useState<CrowdSecSettings | undefined>(undefined)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null)

  useEffect(() => {
    if (!form && cfg.data) {
      const c = cfg.data.crowdsec
      setLoaded(c)
      setForm({
        enabled: c?.enabled ?? false,
        url: c?.url ?? '',
        api_key: '',
        poll: String(c?.poll_seconds || DEFAULT_POLL),
        scope: c?.scope === 'all' ? 'all' : 'wan',
        hasKey: storedKeyOf(c) !== 'none',
      })
    }
  }, [form, cfg.data])

  const save = useMutation({
    mutationFn: (value: CrowdSecSettings) => patchSection('crowdsec', value),
    onSuccess: (res, value) => {
      const errs: Record<string, string> = {}
      for (const e of res.validation_errors ?? []) {
        const field = e.path.startsWith('crowdsec.') ? e.path.slice('crowdsec.'.length) : e.path
        if (field === 'poll_seconds') errs.poll = e.message
        else if (!errs[field]) errs[field] = e.message
      }
      setErrors(errs)
      if (Object.keys(errs).length > 0) {
        setMessage(null)
        return
      }
      // The save went through, so the key that was sent (or kept behind the
      // blank field) is what the server holds now.
      setForm((f) => (f ? { ...f, api_key: '', hasKey: f.hasKey || value.api_key !== undefined } : f))
      // Remember what the server holds now: a key that was sent is the key
      // (echoed back on the next save); one left out stayed as it was.
      setLoaded((c) => ({ ...c, ...value, api_key: value.api_key ?? c?.api_key }))
      setMessage({ ok: true, text: 'Saved. Use the commit bar to apply.' })
      queryClient.invalidateQueries({ queryKey: ['candidate'] })
      queryClient.invalidateQueries({ queryKey: ['crowdsec-status'] })
    },
    onError: (err: Error) => {
      setErrors({})
      setMessage({ ok: false, text: `Could not save: ${err.message}` })
    },
  })

  const set = (patch: Partial<CrowdSecForm>) => {
    setMessage(null)
    setForm((f) => (f ? { ...f, ...patch } : f))
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!form) return
    const errs = validateCrowdSec(form)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    save.mutate(buildCrowdSec(form, loaded))
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">CrowdSec</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400">
          Pull ban decisions from a CrowdSec instance and drop them at the firewall.
        </p>
      </div>

      <StatusCard query={status} />

      <section className="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">Connection</h2>
        {!form ? (
          <p className="text-sm text-gray-500">Loading…</p>
        ) : (
          <form onSubmit={submit} className="max-w-xl space-y-3">
            {message && (
              <div
                role={message.ok ? 'status' : 'alert'}
                className={message.ok ? 'rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300'}
              >
                {message.text}
              </div>
            )}
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" aria-label="Enable CrowdSec" checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
              Enable the CrowdSec bouncer
            </label>

            <label className="block text-sm">
              LAPI URL
              <input
                className={inputClass}
                placeholder="http://192.168.1.20:8080"
                value={form.url}
                onChange={(e) => set({ url: e.target.value })}
              />
            </label>
            {errors.url && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.url}</p>}

            <label className="block text-sm">
              Bouncer key
              <input
                type="password"
                className={inputClass}
                aria-label="Bouncer key"
                placeholder={form.hasKey ? 'unchanged' : ''}
                value={form.api_key}
                onChange={(e) => set({ api_key: e.target.value })}
              />
            </label>
            {errors.api_key && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.api_key}</p>}

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <label className="block text-sm">
                Poll interval (seconds)
                <input
                  type="number"
                  className={inputClass}
                  aria-label="Poll interval"
                  value={form.poll}
                  onChange={(e) => set({ poll: e.target.value })}
                />
              </label>
              <label className="block text-sm">
                Scope
                <select
                  className={inputClass}
                  aria-label="Scope"
                  value={form.scope}
                  onChange={(e) => set({ scope: e.target.value === 'all' ? 'all' : 'wan' })}
                >
                  <option value="wan">WAN only</option>
                  <option value="all">All interfaces</option>
                </select>
              </label>
            </div>
            {errors.poll && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.poll}</p>}
            {errors.scope && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.scope}</p>}

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

// StatusCard shows what the bouncer is doing right now. It takes the plain
// query state so the card can also render the "endpoint unavailable" case.
interface StatusState {
  data?: CrowdSecStatus
  isError: boolean
  error: unknown
}

function StatusCard({ query }: { query: StatusState }) {
  const s = query.data
  const err = query.error
  return (
    <section
      data-testid="crowdsec-status"
      className="space-y-2 rounded-lg border border-gray-200 p-4 dark:border-gray-700"
    >
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold text-gray-800 dark:text-white">Status</h2>
        {!query.isError && s &&
          (s.connected ? (
            <Badge variant="success">Connected</Badge>
          ) : (
            <Badge variant={s.enabled ? 'warning' : 'secondary'}>{s.enabled ? 'Not connected' : 'Disabled'}</Badge>
          ))}
        {query.isError && <Badge variant="error">Unavailable</Badge>}
      </div>
      {query.isError ? (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          Cannot read the CrowdSec status: {err instanceof Error ? err.message : 'unavailable'}
        </p>
      ) : !s ? (
        <p className="text-sm text-gray-500">Loading…</p>
      ) : (
        <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <div>
            <dt className="text-gray-500 dark:text-gray-400">Active decisions</dt>
            <dd className="font-semibold text-gray-900 dark:text-white">{s.active}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">Dropped packets</dt>
            <dd className="font-semibold text-gray-900 dark:text-white">{s.dropped_packets}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">Last pull</dt>
            <dd className="text-gray-900 dark:text-white">{relTime(s.last_pull)}</dd>
          </div>
          <div>
            <dt className="text-gray-500 dark:text-gray-400">Bouncer</dt>
            <dd className="text-gray-900 dark:text-white">{s.enabled ? 'On' : 'Off'}</dd>
          </div>
        </dl>
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
      <h2 className="text-base font-semibold text-gray-800 dark:text-white">Getting a bouncer key</h2>
      <p className="mt-1 text-gray-600 dark:text-gray-300">
        On the CrowdSec machine, create a bouncer and paste the key above:
      </p>
      <pre className="mt-2 overflow-x-auto rounded bg-gray-100 p-2 text-xs dark:bg-gray-800">
        <code>cscli bouncers add goblin-gate</code>
      </pre>
      <p className="mt-2 text-gray-600 dark:text-gray-300">
        Bans land in the nftables sets <code>crowdsec4</code> / <code>crowdsec6</code> and are dropped
        before the firewall policies. See the{' '}
        <a
          className="text-blue-600 underline dark:text-blue-400"
          href={WIKI_URL}
          target="_blank"
          rel="noreferrer"
        >
          CrowdSec section of the wiki
        </a>
        .
      </p>
    </section>
  )
}
