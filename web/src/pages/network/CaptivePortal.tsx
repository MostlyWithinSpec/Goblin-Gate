// Network → Captive portal: the welcome page of guest networks (settings per
// interface or VLAN with a live preview), voucher batches, and the guests
// online now.
import React, { useCallback, useEffect, useState } from 'react'
import { getCandidate, patchSection, type Candidate } from '../../api/objects'
import type { ValidationError } from '../../api/policy'
import { usePermissions } from '../../api/permissions'
import {
  createVoucherBatch,
  deleteVoucherBatch,
  getPortalClients,
  getVoucherBatches,
  kickPortalClient,
  portalPreviewURL,
  revokeVouchers,
  type CaptivePortal as Portal,
  type CodeState,
  type PortalClient,
  type PortalMode,
  type VoucherBatch,
} from '../../api/portal'
import { Dialog } from '../../components/Dialog'
import { inputClass } from '../../components/ConfigList'
import {
  buildPortal,
  formatLeft,
  formatMinutes,
  portalFormOf,
  portalInterfaces,
  portalWikiURL,
  printableBatch,
  readLogo,
  validatePortal,
  type PortalErrors,
  type PortalForm,
} from './portalLogic'
import { t } from '../../i18n'

const box = 'rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300'
const check = 'h-4 w-4 rounded border-gray-300 dark:border-gray-600'
const sub = 'mt-1 block text-xs text-gray-500'
const btn = 'min-h-[40px] rounded border border-gray-300 px-3 text-sm dark:border-gray-600 disabled:opacity-50'
const primary = 'min-h-[40px] rounded bg-blue-600 px-4 text-sm text-white hover:bg-blue-700 disabled:opacity-50'
const errText = 'mt-1 text-xs text-red-600'
const errMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))

const stateCls: Record<CodeState, string> = {
  unused: 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-200',
  in_use: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200',
  used_up: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  expired: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200',
  revoked: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200',
}

function stateLabel(s: CodeState): string {
  return {
    unused: t('portal.state.unused'),
    in_use: t('portal.state.inUse'),
    used_up: t('portal.state.usedUp'),
    expired: t('portal.state.expired'),
    revoked: t('portal.state.revoked'),
  }[s]
}

function modeLabel(m: PortalMode): string {
  return { click: t('portal.mode.click'), password: t('portal.mode.password'), voucher: t('portal.mode.voucher') }[m]
}

// PortalCard edits one portal, with the preview of the saved candidate.
function PortalCard({
  stored,
  canWrite,
  server,
  previewKey,
  onSave,
  onRemove,
}: {
  stored: Portal
  canWrite: boolean
  server: ValidationError[]
  previewKey: number
  onSave: (next: Portal) => Promise<void>
  onRemove: () => void
}) {
  const [f, setF] = useState<PortalForm>(() => portalFormOf(stored))
  const [errors, setErrors] = useState<PortalErrors>({})
  const [logoErr, setLogoErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // a new stored portal (after a save) resets the form; compared during
  // render so that nothing resets it after the first render
  const [shown, setShown] = useState(stored)
  if (stored !== shown) {
    setShown(stored)
    setF(portalFormOf(stored))
  }
  const set = (p: Partial<PortalForm>) => setF({ ...f, ...p })
  const hadPassword = stored.mode === 'password'
  const id = `portal-${stored.interface}`
  const fromServer = (field: string) => server.find((v) => v.path.endsWith('.' + field))?.message
  const msg = (field: keyof PortalErrors, text: string, serverField: string) => {
    const m = errors[field] ? text : fromServer(serverField)
    return m ? (
      <p role="alert" className={errText}>
        {m}
      </p>
    ) : null
  }
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const e = validatePortal(f, hadPassword)
    setErrors(e)
    if (Object.keys(e).length > 0) return
    setBusy(true)
    try {
      await onSave(buildPortal(f))
    } finally {
      setBusy(false)
    }
  }
  const pickLogo = async (file?: File) => {
    setLogoErr(null)
    if (!file) return
    const r = await readLogo(file)
    if (r.error) setLogoErr(r.error === 'size' ? t('portal.errLogoSize') : t('portal.errLogoType'))
    else set({ logo: r.uri ?? '' })
  }
  return (
    <section className={box} aria-labelledby={id}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h3 id={id} className="text-lg font-semibold text-gray-800 dark:text-white">
          {t('portal.on', { iface: stored.interface })}
        </h3>
        {canWrite && (
          <button type="button" className="px-2 text-red-600" aria-label={t('portal.remove', { iface: stored.interface })} onClick={onRemove}>
            {t('portal.removeShort')}
          </button>
        )}
      </div>
      {fromServer('interface') && (
        <p role="alert" className={errText}>
          {fromServer('interface')}
        </p>
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        <form onSubmit={save} className="space-y-3">
          <label className="flex items-center gap-2">
            <input type="checkbox" className={check} checked={f.enabled} disabled={!canWrite} onChange={(e) => set({ enabled: e.target.checked })} />
            {t('portal.enabled')}
          </label>
          <fieldset>
            <legend>{t('portal.mode')}</legend>
            <div className="mt-1 flex flex-wrap gap-4">
              {(['click', 'password', 'voucher'] as PortalMode[]).map((m) => (
                <label key={m} className="flex items-center gap-1">
                  <input type="radio" name={`${id}-mode`} value={m} checked={f.mode === m} disabled={!canWrite} onChange={() => set({ mode: m })} />
                  {modeLabel(m)}
                </label>
              ))}
            </div>
          </fieldset>
          {f.mode === 'password' && (
            <label className="block">
              {t('portal.password')}
              <input
                className={inputClass}
                type="password"
                autoComplete="new-password"
                value={f.password}
                disabled={!canWrite}
                placeholder={hadPassword ? t('portal.passwordUnchanged') : ''}
                onChange={(e) => set({ password: e.target.value })}
              />
              {msg('password', t('portal.errPassword'), 'password')}
            </label>
          )}
          <div className="grid gap-3 sm:grid-cols-3">
            <label className="block">
              {t('portal.session')}
              <input className={inputClass} inputMode="numeric" value={f.session} placeholder="1440" disabled={!canWrite} onChange={(e) => set({ session: e.target.value })} />
              {msg('session', t('portal.errSession'), 'session_minutes')}
            </label>
            <label className="block">
              {t('portal.idle')}
              <input className={inputClass} inputMode="numeric" value={f.idle} placeholder={t('portal.never')} disabled={!canWrite} onChange={(e) => set({ idle: e.target.value })} />
              {msg('idle', t('portal.errIdle'), 'idle_minutes')}
            </label>
            <label className="block">
              {t('portal.rate')}
              <input className={inputClass} inputMode="numeric" value={f.rate} placeholder={t('portal.noLimit')} disabled={!canWrite} onChange={(e) => set({ rate: e.target.value })} />
              {msg('rate', t('portal.errRate'), 'rate_limit_mbps')}
            </label>
          </div>
          <span className={sub}>{t('portal.timesHint')}</span>
          <label className="block">
            {t('portal.pageTitle')}
            <input className={inputClass} maxLength={80} value={f.title} placeholder={t('portal.titlePlaceholder')} disabled={!canWrite} onChange={(e) => set({ title: e.target.value })} />
            {msg('title', t('portal.errTitle'), 'title')}
          </label>
          <label className="block">
            {t('portal.text')}
            <textarea className={inputClass} rows={5} value={f.text} disabled={!canWrite} onChange={(e) => set({ text: e.target.value })} />
            <span className={sub}>{t('portal.textHint')}</span>
            {msg('text', t('portal.errText'), 'text')}
          </label>
          <div>
            <label className="block">
              {t('portal.logo')}
              <input className="mt-1 block text-xs" type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" disabled={!canWrite} onChange={(e) => pickLogo(e.target.files?.[0])} />
            </label>
            <span className={sub}>{t('portal.logoHint')}</span>
            {f.logo && (
              <div className="mt-2 flex items-center gap-3">
                <img src={f.logo} alt={t('portal.logoAlt')} className="max-h-12" />
                {canWrite && (
                  <button type="button" className="text-xs text-red-600" onClick={() => set({ logo: '' })}>
                    {t('portal.logoRemove')}
                  </button>
                )}
              </div>
            )}
            {logoErr && (
              <p role="alert" className={errText}>
                {logoErr}
              </p>
            )}
            {fromServer('logo') && (
              <p role="alert" className={errText}>
                {fromServer('logo')}
              </p>
            )}
          </div>
          {canWrite && (
            <button type="submit" className={primary} disabled={busy}>
              {t('portal.save')}
            </button>
          )}
        </form>
        <div>
          <p className="mb-1 text-xs text-gray-500">{t('portal.previewHint')}</p>
          <iframe
            key={previewKey}
            title={t('portal.previewTitle', { iface: stored.interface })}
            src={`${portalPreviewURL(stored.interface)}?v=${previewKey}`}
            sandbox=""
            className="h-[28rem] w-full rounded border border-gray-200 bg-white dark:border-gray-700"
          />
        </div>
      </div>
    </section>
  )
}

function Vouchers({ portals, canAct }: { portals: Portal[]; canAct: boolean }) {
  const [batches, setBatches] = useState<VoucherBatch[]>([])
  const [error, setError] = useState<string | null>(null)
  const [form, setForm] = useState({ count: '10', minutes: '240', uses: '1', days: '30', note: '', iface: '' })
  const [formErr, setFormErr] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<VoucherBatch | null>(null)
  const load = useCallback(() => {
    getVoucherBatches()
      .then((d) => {
        setBatches(d.batches)
        setError(null)
      })
      .catch((e) => setError(errMessage(e)))
  }, [])
  useEffect(load, [load])
  const voucherPortals = portals.filter((p) => p.mode === 'voucher')
  const make = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setFormErr(null)
    const n = (s: string) => Number(s.trim() || '0')
    if (!(n(form.count) >= 1 && n(form.count) <= 500)) return setFormErr(t('portal.errCount'))
    if (!(n(form.minutes) >= 1 && n(form.minutes) <= 43200)) return setFormErr(t('portal.errMinutes'))
    if (!(n(form.uses) >= 1 && n(form.uses) <= 100)) return setFormErr(t('portal.errUses'))
    if (!(n(form.days) >= 0 && n(form.days) <= 3650)) return setFormErr(t('portal.errDays'))
    try {
      const b = await createVoucherBatch({
        count: n(form.count),
        minutes: n(form.minutes),
        uses: n(form.uses),
        valid_days: n(form.days),
        note: form.note.trim() || undefined,
        interface: form.iface || undefined,
      })
      setOpen(b.id)
      load()
    } catch (e) {
      setFormErr(errMessage(e))
    }
  }
  const print = (b: VoucherBatch) => {
    const html = printableBatch(b, {
      title: b.note || t('portal.printTitle'),
      valid: formatMinutes(b.minutes),
      uses: b.uses === 1 ? t('portal.oneUse') : t('portal.uses', { count: b.uses }),
    })
    const w = window.open('', '_blank')
    if (!w) return
    w.document.write(html)
    w.document.close()
    w.focus()
    w.print()
  }
  const act = async (fn: () => Promise<unknown>) => {
    try {
      await fn()
    } catch (e) {
      setError(errMessage(e))
    }
    load()
  }
  return (
    <section className={box} aria-labelledby="portal-vouchers">
      <h2 id="portal-vouchers" className="mb-3 text-lg font-semibold text-gray-800 dark:text-white">
        {t('portal.vouchers')}
      </h2>
      {voucherPortals.length === 0 && <p className="mb-3 text-xs text-gray-500">{t('portal.noVoucherPortal')}</p>}
      {canAct && (
        <form onSubmit={make} className="mb-4 grid gap-3 sm:grid-cols-3 lg:grid-cols-6" aria-label={t('portal.newBatch')}>
          <label className="block">
            {t('portal.count')}
            <input className={inputClass} inputMode="numeric" value={form.count} onChange={(e) => setForm({ ...form, count: e.target.value })} />
          </label>
          <label className="block">
            {t('portal.minutes')}
            <input className={inputClass} inputMode="numeric" value={form.minutes} onChange={(e) => setForm({ ...form, minutes: e.target.value })} />
          </label>
          <label className="block">
            {t('portal.usesLabel')}
            <input className={inputClass} inputMode="numeric" value={form.uses} onChange={(e) => setForm({ ...form, uses: e.target.value })} />
          </label>
          <label className="block">
            {t('portal.days')}
            <input className={inputClass} inputMode="numeric" value={form.days} onChange={(e) => setForm({ ...form, days: e.target.value })} />
          </label>
          <label className="block">
            {t('portal.batchFor')}
            <select className={inputClass} value={form.iface} onChange={(e) => setForm({ ...form, iface: e.target.value })}>
              <option value="">{t('portal.anyPortal')}</option>
              {voucherPortals.map((p) => (
                <option key={p.interface} value={p.interface}>
                  {p.interface}
                </option>
              ))}
            </select>
          </label>
          <label className="block">
            {t('portal.note')}
            <input className={inputClass} maxLength={80} value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
          </label>
          <div className="sm:col-span-3 lg:col-span-6">
            {formErr && (
              <p role="alert" className="mb-2 text-xs text-red-600">
                {formErr}
              </p>
            )}
            <button type="submit" className={primary}>
              {t('portal.makeBatch')}
            </button>
          </div>
        </form>
      )}
      {error && (
        <p role="alert" className="mb-2 text-red-600">
          {error}
        </p>
      )}
      {batches.length === 0 ? (
        <p className="text-gray-500">{t('portal.noBatches')}</p>
      ) : (
        <ul className="divide-y divide-gray-100 dark:divide-gray-700">
          {batches.map((b) => (
            <li key={b.id} className="py-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <button type="button" className="text-left" aria-expanded={open === b.id} onClick={() => setOpen(open === b.id ? null : b.id)}>
                  <span className="font-medium">{b.note || b.id}</span>{' '}
                  <span className="text-xs text-gray-500">
                    {t('portal.batchSummary', { count: b.codes.length, length: formatMinutes(b.minutes), uses: b.uses })}
                    {b.interface ? ` · ${b.interface}` : ''}
                    {b.expires ? ` · ${t('portal.validUntil', { date: new Date(b.expires).toLocaleDateString() })}` : ''}
                  </span>{' '}
                  {b.revoked ? (
                    <span className={'rounded px-1.5 py-0.5 text-xs ' + stateCls.revoked}>{stateLabel('revoked')}</span>
                  ) : (
                    <span className="text-xs text-gray-500" data-testid={`counts-${b.id}`}>
                      {t('portal.counts', { unused: b.counts.unused ?? 0, used: (b.counts.in_use ?? 0) + (b.counts.used_up ?? 0) })}
                    </span>
                  )}
                </button>
                <div className="flex gap-2">
                  <button type="button" className={btn} aria-label={t('portal.printBatch', { batch: b.note || b.id })} onClick={() => print(b)}>
                    {t('portal.print')}
                  </button>
                  {canAct && !b.revoked && (
                    <button type="button" className={btn} aria-label={t('portal.revokeBatch', { batch: b.note || b.id })} onClick={() => setConfirm(b)}>
                      {t('portal.revoke')}
                    </button>
                  )}
                  {canAct && (
                    <button type="button" className="px-2 text-red-600" aria-label={t('portal.deleteBatch', { batch: b.note || b.id })} onClick={() => act(() => deleteVoucherBatch(b.id))}>
                      {t('portal.delete')}
                    </button>
                  )}
                </div>
              </div>
              {open === b.id && (
                <table className="mt-2 w-full text-left text-xs" aria-label={t('portal.codesOf', { batch: b.note || b.id })}>
                  <thead className="uppercase text-gray-500">
                    <tr>
                      <th className="py-1 pr-3">{t('portal.col.code')}</th>
                      <th className="py-1 pr-3">{t('portal.col.state')}</th>
                      <th className="py-1 pr-3">{t('portal.col.used')}</th>
                      <th className="py-1" />
                    </tr>
                  </thead>
                  <tbody>
                    {b.codes.map((c) => (
                      <tr key={c.code} className="border-t border-gray-100 dark:border-gray-700">
                        <td className="py-1 pr-3 font-mono">{c.code}</td>
                        <td className="py-1 pr-3">
                          <span className={'rounded px-1.5 py-0.5 ' + stateCls[c.state]}>{stateLabel(c.state)}</span>
                        </td>
                        <td className="py-1 pr-3">
                          {c.used}/{b.uses}
                        </td>
                        <td className="py-1 text-right">
                          {canAct && (c.state === 'unused' || c.state === 'in_use') && (
                            <button type="button" className="text-red-600" aria-label={t('portal.revokeCode', { code: c.code })} onClick={() => act(() => revokeVouchers(b.id, c.code))}>
                              {t('portal.revoke')}
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </li>
          ))}
        </ul>
      )}
      <Dialog open={confirm !== null} onClose={() => setConfirm(null)} title={t('portal.revokeTitle')} size="small">
        {confirm && (
          <div className="space-y-3 text-sm">
            <p>{t('portal.revokeBody', { batch: confirm.note || confirm.id })}</p>
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={() => setConfirm(null)}>
                {t('common.cancel')}
              </button>
              <button
                type="button"
                className="min-h-[40px] rounded bg-red-600 px-4 text-sm text-white"
                onClick={() => {
                  const b = confirm
                  setConfirm(null)
                  act(() => revokeVouchers(b.id))
                }}
              >
                {t('portal.revoke')}
              </button>
            </div>
          </div>
        )}
      </Dialog>
    </section>
  )
}

function Guests({ canAct }: { canAct: boolean }) {
  const [clients, setClients] = useState<PortalClient[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback(() => {
    getPortalClients()
      .then((d) => {
        setClients(d.clients)
        setError(null)
      })
      .catch((e) => setError(errMessage(e)))
  }, [])
  useEffect(() => {
    load()
    const id = setInterval(load, 10_000)
    return () => clearInterval(id)
  }, [load])
  const kick = async (c: PortalClient) => {
    try {
      await kickPortalClient(c.mac)
    } catch (e) {
      setError(errMessage(e))
    }
    load()
  }
  return (
    <section className={box} aria-labelledby="portal-guests">
      <h2 id="portal-guests" className="mb-3 text-lg font-semibold text-gray-800 dark:text-white">
        {t('portal.guests')}
      </h2>
      {error && (
        <p role="alert" className="mb-2 text-red-600">
          {error}
        </p>
      )}
      {clients && clients.length === 0 && <p className="text-gray-500">{t('portal.noGuests')}</p>}
      {clients && clients.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm" aria-label={t('portal.guestsTable')}>
            <thead className="text-xs uppercase text-gray-500">
              <tr>
                <th className="py-1 pr-3">{t('portal.col.device')}</th>
                <th className="py-1 pr-3">{t('portal.col.network')}</th>
                <th className="py-1 pr-3">{t('portal.col.how')}</th>
                <th className="py-1 pr-3">{t('portal.col.left')}</th>
                <th className="py-1" />
              </tr>
            </thead>
            <tbody>
              {clients.map((c) => (
                <tr key={c.mac} className="border-t border-gray-100 dark:border-gray-700">
                  <td className="py-2 pr-3">
                    <div className="font-mono text-xs">{c.ip}</div>
                    <div className="font-mono text-xs text-gray-500">{c.mac}</div>
                  </td>
                  <td className="py-2 pr-3">{c.interface}</td>
                  <td className="py-2 pr-3">{c.voucher ? <span className="font-mono text-xs">{c.voucher}</span> : modeLabel(c.method)}</td>
                  <td className="py-2 pr-3">{formatLeft(c.seconds_left)}</td>
                  <td className="py-2 text-right">
                    {canAct && (
                      <button type="button" className="px-2 text-red-600" aria-label={t('portal.kick', { device: c.ip })} onClick={() => kick(c)}>
                        {t('portal.kickShort')}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

const CaptivePortalPage: React.FC = () => {
  const perms = usePermissions()
  const canWrite = perms.can('write:network')
  const canAct = perms.can('actions')
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [server, setServer] = useState<ValidationError[]>([])
  const [adding, setAdding] = useState('')
  const [previewKey, setPreviewKey] = useState(0)

  const load = useCallback(async () => {
    try {
      setCfg(await getCandidate())
      setLoadError(null)
    } catch (e) {
      setLoadError(errMessage(e))
    }
  }, [])
  useEffect(() => {
    load()
  }, [load])

  const portals = cfg?.captive_portals ?? []
  const ifaces = portalInterfaces(cfg ?? undefined)
  const free = ifaces.filter((i) => !portals.some((p) => p.interface === i.name))

  const saveList = async (list: Portal[]) => {
    setSaved(false)
    setSaveError(null)
    try {
      const res = await patchSection('captive_portals', list)
      const mine = (res.validation_errors ?? []).filter((v) => v.path.startsWith('captive_portals'))
      setServer(mine)
      if (mine.length === 0) setSaved(true)
      await load()
      setPreviewKey((k) => k + 1)
    } catch (e) {
      setSaveError(errMessage(e))
    }
  }

  if (loadError) return <div className="text-red-600">{t('portal.loadFailed', { message: loadError })}</div>

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-bold text-gray-800 dark:text-white">{t('portal.title')}</h2>
        <p className="text-sm text-gray-500">
          {t('portal.intro')}{' '}
          <a className="text-blue-600 underline" href={portalWikiURL} target="_blank" rel="noreferrer">
            {t('portal.wiki')}
          </a>
        </p>
      </div>
      {perms.loaded && !canWrite && <p className="text-sm text-gray-500">{t('portal.readOnly')}</p>}
      {saved && (
        <div role="status" className="rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/30 dark:text-green-200">
          {t('portal.saved')}
        </div>
      )}
      {saveError && (
        <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">
          {saveError}
        </div>
      )}

      {portals.length === 0 && <p className="text-sm text-gray-500">{t('portal.none')}</p>}
      {portals.map((p, i) => (
        <PortalCard
          key={p.interface}
          stored={p}
          canWrite={canWrite}
          previewKey={previewKey}
          server={server.filter((v) => v.path.startsWith(`captive_portals[${i}]`))}
          onSave={(next) => saveList(portals.map((o, j) => (j === i ? next : o)))}
          onRemove={() => saveList(portals.filter((_, j) => j !== i))}
        />
      ))}

      {canWrite && free.length > 0 && (
        <div className={box + ' flex flex-wrap items-end gap-3'}>
          <label className="block">
            {t('portal.addFor')}
            <select className={inputClass} value={adding} onChange={(e) => setAdding(e.target.value)}>
              <option value="">{t('portal.pickInterface')}</option>
              {free.map((i) => (
                <option key={i.name} value={i.name} disabled={!i.static}>
                  {i.static ? i.name : t('portal.needsStatic', { iface: i.name })}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className={primary}
            disabled={!adding}
            onClick={() => {
              const name = adding
              setAdding('')
              saveList([...portals, buildPortal(portalFormOf(undefined, name))])
            }}
          >
            {t('portal.add')}
          </button>
        </div>
      )}

      <Vouchers portals={portals} canAct={canAct} />
      <Guests canAct={canAct} />
    </div>
  )
}

export default CaptivePortalPage
