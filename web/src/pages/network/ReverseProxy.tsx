// Network → Reverse Proxy: settings for the built-in proxy (Caddy) that
// publishes LAN services on public names with Let's Encrypt certificates,
// the hosts it publishes, and what it has for certificates right now.
import React, { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCandidate, getProxyStatus, patchSection, type Address, type Candidate, type ProxyHost, type ReverseProxy } from '../../api/objects'
import type { ValidationError } from '../../api/policy'
import { ConfigList, inputClass, ListInput, type Errors, type FormCtx } from '../../components/ConfigList'
import { t } from '../../i18n'

const WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Reverse-Proxy'
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const UPSTREAM_RE = /^https?:\/\/[^/?#@]+$/
const DOMAIN_RE = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/
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

// shortError picks the first server message for one exact path, so it can sit
// next to the field it is about instead of in a list of paths.
function shortError(list: ValidationError[], path: string): string | undefined {
  return list.find((v) => v.path === path)?.message
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

export default function ReverseProxy() {
  const [settings, setSettings] = useState<ReverseProxy | null>(null)
  const [addresses, setAddresses] = useState<Address[]>([])
  const [hosts, setHosts] = useState<ProxyHost[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [errors, setErrors] = useState<Errors>({})
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    getCandidate()
      .then((c: Candidate) => {
        setSettings(c.reverse_proxy ?? { enabled: false })
        setHosts(c.reverse_proxy?.hosts ?? [])
        setAddresses(c.addresses ?? [])
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
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('reverse_proxy')))
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

      <HostErrors list={serverErrors} />

      {/* What the server rejects outside the settings fields and the host list
          (a WAN port forward on 443, for instance) has no field of its own. */}
      {serverErrors.filter((v) => !v.path.startsWith('reverse_proxy.hosts') && !v.path.endsWith('.enabled') && !v.path.endsWith('.email')).length > 0 && (
        <ul role="other-errors" className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {serverErrors
            .filter((v) => !v.path.startsWith('reverse_proxy.hosts') && !v.path.endsWith('.enabled') && !v.path.endsWith('.email'))
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
