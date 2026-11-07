import React, { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  getNetworkConfig,
  getUpnpMappings,
  patchSection,
  type NetIf,
  type UPnpAllow,
  type UPnPConfig,
  type UPnpMapping,
} from '../../api/network'
import type { ValidationError } from '../../api/policy'
import { t } from '../../i18n'
import { portOrRange, type Errors } from './networkLogic'

const input = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'

function Mappings() {
  const [data, setData] = useState<{ enabled: boolean; mappings: UPnpMapping[] } | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = () => {
      getUpnpMappings()
        .then((d) => { if (!cancelled) setData(d) })
        .catch((e: Error) => { if (!cancelled) setError(e.message) })
    }
    load()
    const id = setInterval(load, 10_000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  const fmtExpiry = (iso?: string) => {
    if (!iso) return t('upnp.untilRemoved')
    const diff = new Date(iso).getTime() - Date.now()
    if (diff <= 0) return t('upnp.expiring')
    const s = Math.floor(diff / 1000)
    if (s < 60) return t('upnp.seconds', { count: s })
    const m = Math.floor(s / 60)
    if (m < 60) return t('upnp.minutesSeconds', { minutes: m, seconds: s % 60 })
    const h = Math.floor(m / 60)
    return t('upnp.hoursMinutes', { hours: h, minutes: m % 60 })
  }

  return (
    <section className="mt-6">
      <h3 className="mb-2 font-semibold text-gray-800 dark:text-white">{t('upnp.activeMappings')}</h3>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {!data && !error && <p className="text-sm text-gray-500">{t('common.loading')}</p>}
      {data && data.mappings.length === 0 && (
        <p className="text-sm text-gray-500">{data.enabled ? t('upnp.noMappings') : t('upnp.disabled')}</p>
      )}
      {data && data.mappings.length > 0 && (
        <table className="min-w-full text-sm dark:text-gray-200">
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr><th className="px-2 py-1">{t('upnp.col.protocol')}</th><th className="px-2 py-1">{t('upnp.col.externalPort')}</th><th className="px-2 py-1">{t('upnp.col.internal')}</th><th className="px-2 py-1">{t('upnp.col.description')}</th><th className="px-2 py-1">{t('upnp.col.expires')}</th></tr>
          </thead>
          <tbody>
            {data.mappings.map((m) => (
              <tr key={m.protocol + m.external_port} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-2 py-1">{m.protocol.toUpperCase()}</td>
                <td className="px-2 py-1">{m.external_port}</td>
                <td className="px-2 py-1">{m.internal_ip}:{m.internal_port}</td>
                <td className="px-2 py-1">{m.description}</td>
                <td className="px-2 py-1">{fmtExpiry(m.expires)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

export default function UPnP() {
  const [cfg, setCfg] = useState<UPnPConfig | null>(null)
  const [lanIfs, setLanIfs] = useState<NetIf[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [errors, setErrors] = useState<Errors>({})
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    getNetworkConfig()
      .then((c) => {
        setCfg(c.upnp ?? { enabled: false })
        const wan = new Set((c.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
        setLanIfs(
          [...(c.interfaces ?? []), ...(c.vlans ?? [])]
            .filter((i) => i.mode === 'static' && i.address && !wan.has(i.name)),
        )
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const save = async (next: UPnPConfig) => {
    const prev = cfg
    setCfg(next)
    setError(null)
    setSaved(false)
    try {
      const res = await patchSection('upnp', next)
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('upnp')))
      setSaved(true)
    } catch (e) {
      setCfg(prev)
      setError((e as Error).message)
    }
  }

  const submit = (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!cfg) return
    const errs: Errors = {}
    if (cfg.enabled && (!cfg.interfaces || cfg.interfaces.length === 0)) {
      errs.interfaces = t('upnp.needInterface')
    }
    cfg.allow?.forEach((a, i) => {
      if (!a.ports.trim()) {
        errs[`allow_${i}_ports`] = t('upnp.portsRequired')
      } else if (!portOrRange(a.ports)) {
        errs[`allow_${i}_ports`] = t('upnp.portsInvalid')
      }
    })
    if (cfg.max_lease !== undefined && (cfg.max_lease < 0 || cfg.max_lease > 604800)) {
      errs.max_lease = t('upnp.leaseRange')
    }
    setErrors(errs)
    if (Object.keys(errs).length) return

    const clean: UPnPConfig = { enabled: cfg.enabled }
    if (cfg.enabled) clean.interfaces = cfg.interfaces
    if (cfg.nat_pmp !== undefined) clean.nat_pmp = cfg.nat_pmp
    if (cfg.secure_mode !== undefined) clean.secure_mode = cfg.secure_mode
    const allow = (cfg.allow ?? []).filter((a) => a.address.trim() || a.ports.trim())
    if (allow.length) clean.allow = allow.map((a) => ({ address: a.address.trim(), ports: a.ports.trim() }))
    if (cfg.max_lease && cfg.max_lease > 0) clean.max_lease = cfg.max_lease
    if (cfg.stun) clean.stun = true
    save(clean)
  }

  const toggleIf = (name: string) => {
    setCfg((c) => {
      if (!c) return c
      const cur = c.interfaces ?? []
      return { ...c, interfaces: cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name] }
    })
  }

  const setAllow = (i: number, k: keyof UPnpAllow, v: string) =>
    setCfg((c) => (c ? { ...c, allow: c.allow!.map((a, j) => (j === i ? { ...a, [k]: v } : a)) } : c))

  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>

  if (!cfg) return <div>{error && <p role="alert" className="text-sm text-red-600">{error}</p>}</div>

  return (
    <div>
      <div className="mb-4">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">{t('upnp.title')}</h2>
        <p className="mt-1 text-sm text-gray-500">{t('upnp.intro')}</p>
      </div>
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      {serverErrors.length > 0 && (
        <ul className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {serverErrors.map((v) => <li key={v.path + v.message}><code>{v.path}</code>: {v.message}</li>)}
        </ul>
      )}

      <form onSubmit={submit} noValidate aria-label={t('upnp.settings')} className="space-y-4 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300">
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={cfg.enabled}
            onChange={(e) => setCfg((c) => (c ? { ...c, enabled: e.target.checked } : c))}
            className="h-4 w-4 rounded border-gray-300 dark:border-gray-600"
          />
          <span>{t('upnp.enable')}</span>
        </label>

        {cfg.enabled && (
          <>
            <fieldset>
              <legend className="mb-1 font-medium">{t('upnp.servedInterfaces')}</legend>
              {lanIfs.length === 0 && <p className="text-xs text-gray-500">{t('upnp.noLanInterfaces')}</p>}
              <div className="flex flex-wrap gap-3">
                {lanIfs.map((i) => (
                  <label key={i.name} className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={(cfg.interfaces ?? []).includes(i.name)}
                      onChange={() => toggleIf(i.name)}
                      className="h-4 w-4 rounded border-gray-300 dark:border-gray-600"
                    />
                    <span>{i.name}</span>
                  </label>
                ))}
              </div>
              {err('interfaces')}
            </fieldset>

            <div className="flex flex-wrap gap-6">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={cfg.nat_pmp ?? true}
                  onChange={(e) => setCfg((c) => (c ? { ...c, nat_pmp: e.target.checked } : c))}
                  className="h-4 w-4 rounded border-gray-300 dark:border-gray-600"
                />
                <span>{t('upnp.natPmp')}</span>
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={cfg.secure_mode ?? true}
                  onChange={(e) => setCfg((c) => (c ? { ...c, secure_mode: e.target.checked } : c))}
                  className="h-4 w-4 rounded border-gray-300 dark:border-gray-600"
                />
                <span>{t('upnp.secureMode')}</span>
              </label>
              <label className="flex items-center gap-2" title={t('upnp.stunHint')}>
                <input
                  type="checkbox"
                  checked={cfg.stun ?? false}
                  onChange={(e) => setCfg((c) => (c ? { ...c, stun: e.target.checked } : c))}
                  className="h-4 w-4 rounded border-gray-300 dark:border-gray-600"
                />
                <span>{t('upnp.stun')}</span>
              </label>
            </div>

            <fieldset>
              <legend className="mb-1 font-medium">{t('upnp.allowList')}</legend>
              <p className="mb-2 text-xs text-gray-500">{t('upnp.allowNote')}</p>
              {(cfg.allow ?? []).length > 0 && (
                <table className="min-w-full">
                  <thead className="text-left text-xs uppercase text-gray-500">
                    <tr><th>{t('upnp.col.address')}</th><th>{t('upnp.col.ports')}</th><th></th></tr>
                  </thead>
                  <tbody>
                    {(cfg.allow ?? []).map((a, i) => (
                      <tr key={i}>
                        <td>
                          <input aria-label={t('upnp.allowAddress', { n: i + 1 })} className={input} placeholder={t('upnp.allowAddressPlaceholder')} value={a.address} onChange={(e) => setAllow(i, 'address', e.target.value)} />
                        </td>
                        <td>
                          <input aria-label={t('upnp.allowPorts', { n: i + 1 })} className={input} placeholder={t('upnp.allowPortsPlaceholder')} value={a.ports} onChange={(e) => setAllow(i, 'ports', e.target.value)} />
                          {err(`allow_${i}_ports`)}
                        </td>
                        <td>
                          <button type="button" aria-label={t('upnp.removeAllow', { n: i + 1 })} className="text-red-600" onClick={() => setCfg((c) => (c ? { ...c, allow: c.allow!.filter((_, j) => j !== i) } : c))}>
                            <Trash2 size={14} />
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <button type="button" className="mt-1 flex items-center gap-1 text-sm text-blue-600" onClick={() => setCfg((c) => (c ? { ...c, allow: [...(c.allow ?? []), { address: '', ports: '' }] } : c))}>
                <Plus size={14} /> {t('upnp.addAllowEntry')}
              </button>
            </fieldset>

            <label className="block w-48">
              {t('upnp.maxLease')}
              <input
                aria-label={t('upnp.maxLeaseAria')}
                type="number"
                min={0}
                max={604800}
                placeholder={t('upnp.maxLeasePlaceholder')}
                className={input}
                value={cfg.max_lease ?? ''}
                onChange={(e) => setCfg((c) => (c ? { ...c, max_lease: e.target.value === '' ? undefined : Number(e.target.value) } : c))}
              />
              {err('max_lease')}
            </label>
          </>
        )}

        <div className="flex items-center justify-end gap-3">
          {saved && <span role="status" className="text-green-700 dark:text-green-400">{t('upnp.saved')}</span>}
          <button type="submit" disabled={!loaded} className="rounded bg-blue-600 px-3 py-1.5 text-white hover:bg-blue-700 disabled:opacity-50">
            {t('upnp.save')}
          </button>
        </div>
      </form>

      <Mappings />
    </div>
  )
}
