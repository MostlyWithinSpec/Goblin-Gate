// Network → Reverse Proxy: settings for the built-in proxy (Caddy) that
// publishes LAN services on public names with Let's Encrypt certificates,
// the hosts it publishes, and what it has for certificates right now.
import React, { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  getCandidate,
  getInternalCAStatus,
  getProxyStatus,
  internalCARootURL,
  patchSection,
  type Address,
  type Candidate,
  type InternalCASettings,
  type InternalCAStatus,
  type ProxyHost,
  type ReverseProxy,
} from '../../api/objects'
import type { ValidationError } from '../../api/policy'
import { ConfigList, inputClass, ListInput, type Errors, type FormCtx } from '../../components/ConfigList'
import { t } from '../../i18n'

const WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Reverse-Proxy'
const CA_WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Internal-CA'
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const UPSTREAM_RE = /^https?:\/\/[^/?#@]+$/
const DOMAIN_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/
const ISSUER_INTERNAL = 'internal'
// A CA name is free text (config.maxInternalCANameLength); the server rejects
// control characters, quotes and backslashes.
const CA_NAME_MAX = 64
const CA_NAME_RE = /^[\x20-\x21\x23-\x5b\x5d-\x7e]*$/
const LIFETIME_MIN = 1
const LIFETIME_MAX = 120
const LIFETIME_DEFAULT = 48
// An allow entry is an address object's name, an address or a CIDR. The server
// resolves the name; here only "not blank, no spaces" can be checked.
const ALLOW_RE = /^[A-Za-z0-9_.:/-]+$/

function expiry(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString()
}

// Status shows what the box has published (GET /api/v1/reverse-proxy), refreshed
// every 10 s while the page is open.
function Status({ hosts }: { hosts: ProxyHost[] }) {
  const q = useQuery({ queryKey: ['reverse-proxy-status'], queryFn: getProxyStatus, refetchInterval: 10_000, retry: false })
  const st = q.data
  const byName = new Map((st?.hosts ?? []).map((h) => [h.name, h]))
  const shown = hosts.length > 0 ? hosts : (st?.hosts ?? []).map((h) => ({ name: h.name }) as ProxyHost)
  const badge = !st || !st.enabled || st.state === 'active' ? 'ok' : st.state === 'unknown' ? 'muted' : 'bad'
  const cls =
    badge === 'ok'
      ? 'rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-800 dark:bg-green-900/40 dark:text-green-200'
      : badge === 'bad'
        ? 'rounded bg-red-100 px-1.5 py-0.5 text-xs text-red-700 dark:bg-red-900/40 dark:text-red-200'
        : 'rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  return (
    <section className="mt-6">
      <h3 className="mb-2 font-semibold text-gray-800 dark:text-white">{t('proxy.certificates')}</h3>
      {q.isError && <p role="alert" className="text-sm text-red-600">{(q.error as Error).message}</p>}
      {!q.data && !q.isError && <p className="text-sm text-gray-500">{t('common.loading')}</p>}
      {st && (
        <>
          <p className="mb-2 text-sm text-gray-600 dark:text-gray-400">
            <span className={cls}>{st.state}</span>{' '}
            {!st.enabled && st.state === 'inactive' && <span>{t('proxy.serviceStopped')}</span>}
          </p>
          {shown.length === 0 && <p className="text-sm text-gray-500">{t('proxy.noHostsYet')}</p>}
          {shown.length > 0 && (
            <table className="min-w-full text-sm dark:text-gray-200" aria-label={t('proxy.certificates')}>
              <thead className="text-left text-xs uppercase text-gray-500">
                <tr>
                  <th className="px-2 py-1">{t('proxy.col.domain')}</th>
                  <th className="px-2 py-1">{t('proxy.col.certificate')}</th>
                </tr>
              </thead>
              <tbody>
                {shown.flatMap((h) => {
                  const hs = byName.get(h.name)
                  if (!hs) return [<tr key={h.name}><td className="px-2 py-1" colSpan={2}><span className="text-gray-500">{t('proxy.commitToPublish')}</span></td></tr>]
                  return hs.domains.map((d) => (
                    <tr key={h.name + d.domain} className="border-t border-gray-100 dark:border-gray-700">
                      <td className="px-2 py-1">
                        {d.domain}
                        {hs.enabled || !st.enabled ? null : <span className="ml-2 text-xs text-gray-500">{t('proxy.disabledHost')}</span>}
                      </td>
                      <td className="px-2 py-1">
                        {d.certificate ? (
                          <span>
                            {d.certificate.issuer} · {t('proxy.expires', { date: expiry(d.certificate.not_after) })}
                            {d.certificate.staging && <span className="ml-2 rounded bg-yellow-100 px-1.5 py-0.5 text-xs text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200">{t('proxy.stagingBadge')}</span>}
                          </span>
                        ) : d.error ? (
                          <span className="text-red-600">{new Date(d.error.time).toLocaleDateString()}: {d.error.message}</span>
                        ) : (
                          <span className="text-gray-500">{t('proxy.waiting')}</span>
                        )}
                      </td>
                    </tr>
                  ))
                })}
              </tbody>
            </table>
          )}
        </>
      )}
    </section>
  )
}

// matchesPath says whether a server message is about one field, whether it is
// indexed ("internal_ca.acme_allow[0]") or not.
function matchesPath(path: string, field: string): boolean {
  return path === field || path.startsWith(field + '[')
}

// shortError picks the first server message for one of the given full paths, so
// it can sit next to the field it is about instead of in a list of paths. The
// settings card matches reverse_proxy.* and the CA card internal_ca.*.
function shortError(list: ValidationError[], ...fields: string[]): string | undefined {
  for (const f of fields) {
    const hit = list.find((v) => matchesPath(v.path, f))
    if (hit) return hit.message
  }
  return undefined
}

// HostErrors shows what the server rejects about the saved host list. The
// settings card is what saves that list, so its messages go under the card
// rather than in the table's own error list (which shows what editing one row
// broke).
function HostErrors({ list }: { list: ValidationError[] }) {
  const first = new Map<string, string>()
  for (const v of list) if (v.path.startsWith('reverse_proxy.hosts') && !first.has(v.path)) first.set(v.path, v.message)
  if (first.size === 0) return null
  return (
    <ul role="host-errors" className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
      {[...first.entries()].map(([path, message]) => (
        <li key={path}>{message}</li>
      ))}
    </ul>
  )
}

// mergeErrors replaces the server messages of one config section with a fresh
// list, leaving the other section's alone. The page has two cards that save
// different sections (reverse_proxy and internal_ca) into one error state.
function mergeErrors(have: ValidationError[], prefix: string, fresh: ValidationError[]): ValidationError[] {
  return [...have.filter((v) => !v.path.startsWith(prefix)), ...fresh.filter((v) => v.path.startsWith(prefix))]
}

// caFields lists the card's own fields, so a server message about one of them
// sits under its input instead of in the card's leftover list.
const caFields = ['internal_ca.name', 'internal_ca.acme', 'internal_ca.acme_allow', 'internal_ca.lifetime_hours', 'internal_ca.enabled']

// CAServerErrors shows what the server rejects about the CA outside the card's
// own fields (a network that overlaps the address the box keeps for internal
// proxy hosts, for instance), so the message is not lost.
function CAServerErrors({ list }: { list: ValidationError[] }) {
  const rest = list.filter((v) => v.path === 'internal_ca' || (v.path.startsWith('internal_ca.') && !caFields.some((f) => matchesPath(v.path, f))))
  if (rest.length === 0) return null
  return (
    <ul role="ca-errors" className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
      {rest.map((v) => (
        <li key={v.path + v.message}>
          <code>{v.path}</code>: {v.message}
        </li>
      ))}
    </ul>
  )
}

// caFormOf turns the stored section into the card's form. A lifetime that was
// never set gets the default, so the field never shows an empty number.
function caFormOf(c: InternalCASettings | undefined) {
  return {
    enabled: c?.enabled ?? false,
    name: c?.name ?? '',
    acme: c?.acme ?? false,
    acmeAllow: c?.acme_allow ?? [],
    lifetime: String(c?.lifetime_hours || LIFETIME_DEFAULT),
  }
}
type CAForm = ReturnType<typeof caFormOf>

// buildCA turns the form back into the candidate object, leaving out the
// defaults so the stored config stays small.
function buildCA(f: CAForm): InternalCASettings {
  const out: InternalCASettings = { enabled: f.enabled }
  const name = f.name.trim()
  if (name) out.name = name
  if (f.enabled && f.acme) {
    out.acme = true
    const allow = f.acmeAllow.map((a) => a.trim()).filter(Boolean)
    if (allow.length) out.acme_allow = allow
  }
  const hours = Number(f.lifetime.trim())
  if (f.lifetime.trim() !== '' && Number.isFinite(hours) && Math.trunc(hours) !== LIFETIME_DEFAULT) {
    out.lifetime_hours = Math.trunc(hours)
  }
  return out
}

// validateCA mirrors the server rules (config.checkInternalCA) for what can be
// checked in a browser, so a bad save shows next to its field.
function validateCA(f: CAForm): Errors {
  const e: Errors = {}
  const name = f.name.trim()
  if (name.length > CA_NAME_MAX || !CA_NAME_RE.test(name)) e.name = t('ca.nameInvalid')
  const raw = f.lifetime.trim()
  if (raw !== '') {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < LIFETIME_MIN || n > LIFETIME_MAX) e.lifetime = t('ca.lifetimeInvalid')
  }
  if (f.acmeAllow.some((a) => !ALLOW_RE.test(a.trim()))) e.acmeAllow = t('proxy.allowInvalid')
  return e
}

// CertificateAuthority is the CA card: the settings at candidate path
// internal_ca, and below them what the running CA has made (GET
// /api/v1/internal-ca): the root's fingerprint and expiry, the ACME directory
// URLs to copy into a client, and the root download links. The links need no
// login and answer 404 until the root exists, so they only appear when ready.
function CertificateAuthority({
  settings,
  setSettings,
  addresses,
  loaded,
  serverErrors,
  setServerErrors,
}: {
  settings: InternalCASettings
  setSettings: (next: InternalCASettings) => void
  addresses: Address[]
  loaded: boolean
  serverErrors: ValidationError[]
  setServerErrors: (list: ValidationError[]) => void
}) {
  const form = caFormOf(settings)
  const [errors, setErrors] = useState<Errors>({})
  const [saved, setSaved] = useState(false)
  const [copied, setCopied] = useState(false)
  const status = useQuery({ queryKey: ['internal-ca-status'], queryFn: getInternalCAStatus, refetchInterval: 10_000, retry: false })
  const st = status.data

  // set edits one form field. The card keeps the candidate section as its state
  // (so the host drawer can see whether the CA is on), which means the two
  // fields whose form name differs from the config's need mapping here.
  const set = (patch: Partial<CAForm>) => {
    setSaved(false)
    setCopied(false)
    const next: InternalCASettings = { ...settings }
    if (patch.enabled !== undefined) next.enabled = patch.enabled
    if (patch.name !== undefined) next.name = patch.name
    if (patch.acme !== undefined) next.acme = patch.acme
    if (patch.acmeAllow !== undefined) next.acme_allow = patch.acmeAllow
    if (patch.lifetime !== undefined) {
      const n = Number(patch.lifetime.trim())
      next.lifetime_hours = Number.isFinite(n) ? Math.trunc(n) : undefined
    }
    setSettings(next)
  }

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const errs = validateCA(form)
    setErrors(errs)
    setSaved(false)
    if (Object.keys(errs).length) return
    try {
      const res = await patchSection('internal_ca', buildCA(form))
      setServerErrors(mergeErrors(serverErrors, 'internal_ca', res.validation_errors ?? []))
      setSaved(true)
    } catch (e) {
      setErrors({ save: (e as Error).message })
    }
  }

  const copyDirectory = async () => {
    const url = st?.acme_directories?.[0] ?? ''
    if (!url) return
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  const err = (k: keyof Errors) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>
  const caErr = (field: string) => {
    const m = shortError(serverErrors, 'internal_ca.' + field)
    return m && <p role="alert" className="mt-1 text-xs text-red-600">{m}</p>
  }

  return (
    <section className="mb-6">
      <h3 className="mb-1 font-semibold text-gray-800 dark:text-white">{t('ca.card')}</h3>
      <p className="mb-3 text-sm text-gray-500">
        {t('ca.intro')}{' '}
        <a className="text-blue-600 underline dark:text-blue-400" href={CA_WIKI_URL} target="_blank" rel="noreferrer">
          {t('ca.wikiLink')}
        </a>
        .
      </p>
      <form onSubmit={save} noValidate aria-label={t('ca.settings')} className="space-y-4 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300">
        <label className="flex items-center gap-2">
          <input type="checkbox" aria-label={t('ca.enable')} checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} className="h-4 w-4 rounded border-gray-300 dark:border-gray-600" />
          <span>{t('ca.enable')}</span>
        </label>
        {caErr('enabled')}
        {form.enabled && (
          <>
            <label className="block w-80 max-w-full">
              {t('ca.name')}
              <input aria-label={t('ca.name')} className={inputClass} placeholder="Goblin Gate Local CA" value={form.name} onChange={(e) => set({ name: e.target.value })} />
              {err('name')}
              {caErr('name')}
              <p className="mt-1 text-xs text-gray-500">{t('ca.nameNote')}</p>
            </label>
            <label className="block">
              <span className="flex items-center gap-2">
                <input type="checkbox" aria-label={t('ca.acme')} checked={form.acme} onChange={(e) => set({ acme: e.target.checked })} className="h-4 w-4 rounded border-gray-300 dark:border-gray-600" />
                <span>{t('ca.acme')}</span>
              </span>
              <p className="mt-1 text-xs text-gray-500">{t('ca.acmeNote')}</p>
              {caErr('acme')}
            </label>
            {form.acme && (
              <fieldset>
                <legend>{t('ca.acmeAllow')}</legend>
                <p className="mb-1 text-xs text-gray-500">{t('ca.acmeAllowNote')}</p>
                <select
                  aria-label={t('ca.acmeAllowPick')}
                  className={inputClass}
                  value=""
                  onChange={(e) => {
                    const name = e.target.value
                    if (!name) return
                    set({ acmeAllow: [...form.acmeAllow, name] })
                  }}
                >
                  <option value="">{t('proxy.allowedPickPlaceholder')}</option>
                  {addresses
                    .filter((a) => a.type === 'host' || a.type === 'subnet')
                    .map((a) => (
                      <option key={a.name} value={a.name}>
                        {a.name} ({a.value})
                      </option>
                    ))}
                </select>
                {form.acmeAllow.length > 0 && (
                  <ul className="mt-2 space-y-1">
                    {form.acmeAllow.map((a, i) => (
                      <li key={a + i} className="flex items-center justify-between gap-2 rounded border border-gray-200 px-2 py-1 dark:border-gray-600">
                        <span className="font-mono text-xs">{a}</span>
                        <button type="button" aria-label={t('ca.removeAllow', { n: i + 1 })} className="text-red-600" onClick={() => set({ acmeAllow: form.acmeAllow.filter((_, j) => j !== i) })}>
                          ✕
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
                <label className="mt-2 block">
                  <span className="text-xs text-gray-500">{t('proxy.allowCidr')}</span>
                  <input
                    aria-label={t('ca.acmeAllowCidr')}
                    className={inputClass}
                    placeholder={t('proxy.allowCidrPlaceholder')}
                    value=""
                    onChange={(e) => {
                      const v = e.target.value.trim()
                      if (v) set({ acmeAllow: [...form.acmeAllow, v] })
                    }}
                  />
                </label>
                {err('acmeAllow')}
                {caErr('acme_allow')}
              </fieldset>
            )}
            <label className="block w-40 max-w-full">
              {t('ca.lifetime')}
              <input
                type="number"
                aria-label={t('ca.lifetime')}
                min={LIFETIME_MIN}
                max={LIFETIME_MAX}
                placeholder={String(LIFETIME_DEFAULT)}
                className={inputClass}
                value={form.lifetime}
                onChange={(e) => set({ lifetime: e.target.value })}
              />
              {err('lifetime')}
              {caErr('lifetime_hours')}
              <p className="mt-1 text-xs text-gray-500">{t('ca.lifetimeNote')}</p>
            </label>
          </>
        )}
        <div className="flex items-center justify-end gap-3">
          {errors.save && <span role="alert" className="text-sm text-red-600">{errors.save}</span>}
          {saved && <span role="status" className="text-green-700 dark:text-green-400">{t('proxy.saved')}</span>}
          <button type="submit" disabled={!loaded} className="rounded bg-blue-600 px-3 py-1.5 text-white hover:bg-blue-700 disabled:opacity-50">
            {t('proxy.save')}
          </button>
        </div>
      </form>

      <CAServerErrors list={serverErrors} />

      <div className="mt-3 space-y-2 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300" aria-label={t('ca.root')}>
        {status.isError && <p role="alert" className="text-sm text-red-600">{(status.error as Error).message}</p>}
        {!st && !status.isError && <p className="text-sm text-gray-500">{t('common.loading')}</p>}
        {st && !st.enabled && <p className="text-sm text-gray-500">{t('ca.off')}</p>}
        {st && st.enabled && !st.ready && (
          <p role="status" aria-label={t('ca.notReady')} className="text-sm text-gray-500">
            {t('ca.notReady')}
          </p>
        )}
        {st?.ready && st.root && (
          <dl className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <div>
              <dt className="text-xs uppercase text-gray-500">{t('ca.subject')}</dt>
              <dd className="text-gray-900 dark:text-white">{st.root.subject}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase text-gray-500">{t('ca.expires')}</dt>
              <dd className="text-gray-900 dark:text-white">{expiry(st.root.not_after)}</dd>
            </div>
            <div className="sm:col-span-2">
              <dt className="text-xs uppercase text-gray-500">{t('ca.fingerprint')}</dt>
              <dd>
                <code className="break-all font-mono text-xs">{st.root.sha256}</code>
                <p className="mt-1 text-xs text-gray-500">{t('ca.fingerprintNote')}</p>
              </dd>
            </div>
          </dl>
        )}
        {st?.ready && (
          <div className="flex flex-wrap items-center gap-3">
            <a
              className="rounded bg-blue-600 px-3 py-1.5 text-white hover:bg-blue-700"
              href={internalCARootURL()}
              download
            >
              {t('ca.downloadPem')}
            </a>
            <a
              className="rounded border border-blue-600 px-3 py-1.5 text-blue-700 hover:bg-blue-50 dark:text-blue-300 dark:hover:bg-blue-900/30"
              href={internalCARootURL('der')}
              download
            >
              {t('ca.downloadDer')}
            </a>
            <span className="text-xs text-gray-500">{t('ca.downloadNote')}</span>
          </div>
        )}
        {st && st.acme_directories.length > 0 && (
          <div>
            <p className="text-xs uppercase text-gray-500">{t('ca.acmeDirectories')}</p>
            <ul className="mt-1 space-y-1">
              {st.acme_directories.map((u) => (
                <li key={u} className="flex flex-wrap items-center gap-2">
                  <code className="break-all font-mono text-xs">{u}</code>
                </li>
              ))}
            </ul>
            <button type="button" onClick={copyDirectory} className="mt-1 rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-50 dark:border-gray-600 dark:hover:bg-gray-700">
              {copied ? t('ca.copied') : t('ca.copyDirectory')}
            </button>
          </div>
        )}
        {st && st.internal_hosts.length > 0 && (
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t('ca.internalHosts')}{' '}
            <span className="font-mono text-xs">{st.internal_hosts.join(', ')}</span>
          </p>
        )}
      </div>
    </section>
  )
}

export default function ReverseProxy() {
  const [settings, setSettings] = useState<ReverseProxy | null>(null)
  const [addresses, setAddresses] = useState<Address[]>([])
  const [hosts, setHosts] = useState<ProxyHost[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [errors, setErrors] = useState<Errors>({})
  const [saved, setSaved] = useState(false)
  const [ca, setCa] = useState<InternalCASettings>({ enabled: false })

  useEffect(() => {
    getCandidate()
      .then((c: Candidate) => {
        setSettings(c.reverse_proxy ?? { enabled: false })
        setHosts(c.reverse_proxy?.hosts ?? [])
        setAddresses(c.addresses ?? [])
        setCa(c.internal_ca ?? { enabled: false })
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const save = async (next: ReverseProxy) => {
    const prev = settings
    setSettings(next)
    setHosts(next.hosts ?? [])
    setError(null)
    setSaved(false)
    try {
      const res = await patchSection('reverse_proxy', next)
      // keep what the CA card has: one card's save must not clear the other's
      setServerErrors(mergeErrors(serverErrors, 'reverse_proxy', res.validation_errors ?? []))
      setSaved(true)
    } catch (e) {
      setSettings(prev)
      setHosts(prev?.hosts ?? [])
      setError((e as Error).message)
    }
  }

  const submit = (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!settings) return
    const errs: Errors = {}
    const email = settings.email?.trim() ?? ''
    if (email && !EMAIL_RE.test(email)) errs.email = t('proxy.emailInvalid')
    setErrors(errs)
    if (Object.keys(errs).length) return
    const clean: ReverseProxy = { enabled: settings.enabled }
    if (email) clean.email = email
    if (settings.staging) clean.staging = true
    if (settings.lan_access === false) clean.lan_access = false
    clean.hosts = hosts
    save(clean)
  }

  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>
  if (!settings) return <div>{error && <p role="alert" className="text-sm text-red-600">{error}</p>}</div>

  return (
    <div>
      <div className="mb-4">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">{t('proxy.title')}</h2>
        <p className="mt-1 text-sm text-gray-500">
          {t('proxy.intro')}{' '}
          <a className="text-blue-600 underline dark:text-blue-400" href={WIKI_URL} target="_blank" rel="noreferrer">
            {t('proxy.wikiLink')}
          </a>
          .
        </p>
      </div>
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">{error}</div>}

      <form onSubmit={submit} noValidate aria-label={t('proxy.settings')} className="mb-6 space-y-4 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={settings.enabled} onChange={(e) => setSettings((c) => (c ? { ...c, enabled: e.target.checked } : c))} className="h-4 w-4 rounded border-gray-300 dark:border-gray-600" />
          <span>{t('proxy.enable')}</span>
        </label>
        {shortError(serverErrors, 'reverse_proxy.enabled') && (
          <p role="alert" className="text-xs text-red-600">{shortError(serverErrors, 'reverse_proxy.enabled')}</p>
        )}
        <label className="block w-80 max-w-full">
          {t('proxy.email')}
          <input aria-label={t('proxy.email')} type="email" placeholder={t('proxy.emailPlaceholder')} className={inputClass} value={settings.email ?? ''} onChange={(e) => setSettings((c) => (c ? { ...c, email: e.target.value } : c))} />
          {err('email')}
          {shortError(serverErrors, 'reverse_proxy.email') && (
            <p role="alert" className="mt-1 text-xs text-red-600">{shortError(serverErrors, 'reverse_proxy.email')}</p>
          )}
          <p className="mt-1 text-xs text-gray-500">{t('proxy.emailNote')}</p>
        </label>
        <label className="block">
          <span className="flex items-center gap-2">
            <input type="checkbox" checked={settings.staging ?? false} onChange={(e) => setSettings((c) => (c ? { ...c, staging: e.target.checked } : c))} className="h-4 w-4 rounded border-gray-300 dark:border-gray-600" />
            <span>{t('proxy.staging')}</span>
          </span>
          <p className="mt-1 text-xs text-gray-500">{t('proxy.stagingNote')}</p>
        </label>
        <label className="block">
          <span className="flex items-center gap-2">
            <input type="checkbox" checked={settings.lan_access !== false} onChange={(e) => setSettings((c) => (c ? { ...c, lan_access: e.target.checked } : c))} className="h-4 w-4 rounded border-gray-300 dark:border-gray-600" />
            <span>{t('proxy.lanAccess')}</span>
          </span>
          <p className="mt-1 text-xs text-gray-500">{t('proxy.lanAccessNote')}</p>
        </label>
        <div className="flex items-center justify-end gap-3">
          {saved && <span role="status" className="text-green-700 dark:text-green-400">{t('proxy.saved')}</span>}
          <button type="submit" disabled={!loaded} className="rounded bg-blue-600 px-3 py-1.5 text-white hover:bg-blue-700 disabled:opacity-50">
            {t('proxy.save')}
          </button>
        </div>
      </form>

      <CertificateAuthority
        settings={ca}
        setSettings={setCa}
        addresses={addresses}
        loaded={loaded}
        serverErrors={serverErrors}
        setServerErrors={setServerErrors}
      />

      <HostErrors list={serverErrors} />

      {/* What the server rejects outside the settings fields and the host list
          (a WAN port forward on 443, for instance) has no field of its own. The
          CA card shows its own messages. */}
      {serverErrors.filter((v) => !v.path.startsWith('internal_ca') && !v.path.startsWith('reverse_proxy.hosts') && !v.path.endsWith('.enabled') && !v.path.endsWith('.email')).length > 0 && (
        <ul role="other-errors" className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {serverErrors
            .filter((v) => !v.path.startsWith('internal_ca') && !v.path.startsWith('reverse_proxy.hosts') && !v.path.endsWith('.enabled') && !v.path.endsWith('.email'))
            .map((v) => (
              <li key={v.path + v.message}>
                <code>{v.path}</code>: {v.message}
              </li>
            ))}
        </ul>
      )}

      <ConfigList<ProxyHost>
        title={t('proxy.hosts')}
        path="reverse_proxy.hosts"
        select={(cfg) => cfg.reverse_proxy?.hosts}
        empty={() => ({ name: '', enabled: true, domains: [], upstream: '' })}
        newLabel={t('proxy.createHost')}
        columns={[
          { label: t('proxy.col.domains'), render: (h) => h.domains.join(', ') },
          { label: t('proxy.col.issuer'), render: (h) => (h.issuer === ISSUER_INTERNAL ? t('proxy.issuerInternal') : "Let's Encrypt") },
          { label: t('proxy.col.upstream'), render: (h) => <code className="text-xs">{h.upstream}</code> },
          { label: t('proxy.col.allowed'), render: (h) => (h.allow?.length ? h.allow.join(', ') : t('proxy.everyone')) },
          { label: t('proxy.col.enabled'), render: (h) => (h.enabled ? t('proxy.enabled') : t('proxy.disabledHost')) },
        ]}
        validate={(item) => {
          const errs: Errors = {}
          if (!item.domains.length) errs.domains = t('proxy.domainsRequired')
          else if (item.domains.some((d) => !DOMAIN_RE.test(d))) errs.domains = t('proxy.domainsInvalid')
          if (!item.upstream.trim()) errs.upstream = t('proxy.upstreamRequired')
          else if (!UPSTREAM_RE.test(item.upstream.trim())) errs.upstream = t('proxy.upstreamInvalid')
          if (item.insecure_skip_verify && !item.upstream.trim().toLowerCase().startsWith('https://')) {
            errs.insecure_skip_verify = t('proxy.insecureHttpsOnly')
          }
          if ((item.allow ?? []).some((a) => !ALLOW_RE.test(a.trim()))) errs.allow = t('proxy.allowInvalid')
          return errs
        }}
        clean={(h) => {
          const out: ProxyHost = {
            name: h.name.trim(),
            enabled: h.enabled,
            domains: h.domains.map((d) => d.trim().toLowerCase()).filter(Boolean),
            upstream: h.upstream.trim(),
          }
          if (h.insecure_skip_verify && out.upstream.toLowerCase().startsWith('https://')) out.insecure_skip_verify = true
          if (h.issuer === ISSUER_INTERNAL) out.issuer = ISSUER_INTERNAL
          const allow = (h.allow ?? []).map((a) => a.trim()).filter(Boolean)
          if (allow.length) out.allow = allow
          if (h.description?.trim()) out.description = h.description.trim()
          return out
        }}
        form={(item, set, ctx: FormCtx) => (
          <>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={item.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> {t('proxy.enabled')}
            </label>
            <ListInput label={t('proxy.domains')} value={item.domains} onChange={(v) => set({ domains: v })} placeholder={t('proxy.domainsPlaceholder')} />
            {ctx.err('domains')}
            {ctx.cfg.internal_ca?.enabled && (
              <label className="block">
                {t('proxy.issuer')}
                <select
                  aria-label={t('proxy.issuer')}
                  className={inputClass}
                  value={item.issuer === ISSUER_INTERNAL ? ISSUER_INTERNAL : ''}
                  onChange={(e) => set({ issuer: e.target.value === ISSUER_INTERNAL ? ISSUER_INTERNAL : '' })}
                >
                  <option value="">{t('proxy.issuerLetsEncrypt')}</option>
                  <option value={ISSUER_INTERNAL}>{t('proxy.issuerInternal')}</option>
                </select>
                <p className="mt-1 text-xs text-gray-500">
                  {item.issuer === ISSUER_INTERNAL ? t('proxy.issuerInternalNote') : t('proxy.issuerLetsEncryptNote')}
                </p>
              </label>
            )}
            <label className="block">
              {t('proxy.upstream')}
              <input aria-label={t('proxy.upstream')} placeholder="http://192.168.1.10:8096" className={inputClass} value={item.upstream ?? ''} onChange={(e) => set({ upstream: e.target.value })} />
              {ctx.err('upstream')}
            </label>
            {item.upstream.trim().toLowerCase().startsWith('https://') && (
              <label className="block">
                <span className="flex items-center gap-2">
                  <input type="checkbox" checked={item.insecure_skip_verify ?? false} onChange={(e) => set({ insecure_skip_verify: e.target.checked })} />
                  <span>{t('proxy.insecure')}</span>
                </span>
                <p className="mt-1 text-xs text-gray-500">{t('proxy.insecureNote')}</p>
                {ctx.err('insecure_skip_verify')}
              </label>
            )}
            <fieldset className="block">
              <legend>{t('proxy.allowed')}</legend>
              <p className="mb-1 text-xs text-gray-500">{t('proxy.allowedNote')}</p>
              <select
                aria-label={t('proxy.allowedPick')}
                className={inputClass}
                value=""
                onChange={(e) => {
                  const name = e.target.value
                  if (!name) return
                  set({ allow: [...(item.allow ?? []), name] })
                }}
              >
                <option value="">{t('proxy.allowedPickPlaceholder')}</option>
                {addresses
                  .filter((a) => a.type === 'host' || a.type === 'subnet')
                  .map((a) => (
                    <option key={a.name} value={a.name}>
                      {a.name} ({a.value})
                    </option>
                  ))}
              </select>
              {(item.allow ?? []).length > 0 && (
                <ul className="mt-2 space-y-1">
                  {(item.allow ?? []).map((a, i) => (
                    <li key={a + i} className="flex items-center justify-between gap-2 rounded border border-gray-200 px-2 py-1 dark:border-gray-600">
                      <span className="font-mono text-xs">{a}</span>
                      <button type="button" aria-label={t('proxy.removeSource', { n: i + 1 })} className="text-red-600" onClick={() => set({ allow: (item.allow ?? []).filter((_, j) => j !== i) })}>
                        ✕
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              <label className="mt-2 block">
                <span className="text-xs text-gray-500">{t('proxy.allowCidr')}</span>
                <input
                  aria-label={t('proxy.allowCidr')}
                  className={inputClass}
                  placeholder={t('proxy.allowCidrPlaceholder')}
                  value=""
                  onChange={(e) => {
                    const v = e.target.value.trim()
                    if (v) set({ allow: [...(item.allow ?? []), v] })
                  }}
                />
              </label>
              {ctx.err('allow')}
            </fieldset>
            <label className="block">
              {t('proxy.description')}
              <input aria-label={t('proxy.description')} className={inputClass} value={item.description ?? ''} onChange={(e) => set({ description: e.target.value })} />
            </label>
          </>
        )}
        after={(cfg) => <Status hosts={cfg.reverse_proxy?.hosts ?? []} />}
      />
    </div>
  )
}
