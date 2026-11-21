// Network → RADIUS: the switches and access points that ask, MAC
// authentication and 802.1X, the default for unknown devices, groups and
// users; per-device Wi-Fi passwords; the recent authentications and the
// sessions.
import React, { useCallback, useEffect, useState } from 'react'
import { getCandidate, patchSection, type Candidate } from '../../api/objects'
import type { ValidationError } from '../../api/policy'
import { usePermissions } from '../../api/permissions'
import {
  deletePPSK,
  disconnectSession,
  getPPSKs,
  getRadiusLog,
  getRadiusSessions,
  issueCertificate,
  revokeCertificates,
  setPPSK,
  type AuthEvent,
  type ClientCert,
  type PPSKEntry,
  type RadiusSession,
} from '../../api/radius'
import { Dialog } from '../../components/Dialog'
import { Badge } from '../../components/Badge'
import { inputClass } from '../../components/ConfigList'
import {
  buildRadius,
  colonMAC,
  filterEvents,
  newClient,
  newGroup,
  newUser,
  pemDownload,
  radiusFormOf,
  radiusWikiURL,
  validateRadius,
  vlanNames,
  type RadiusErrors,
  type RadiusForm,
  type ResultFilter,
} from './radiusLogic'
import { t } from '../../i18n'

const box = 'rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300'
const check = 'h-4 w-4 rounded border-gray-300 dark:border-gray-600'
const btn = 'min-h-[40px] rounded border border-gray-300 px-3 text-sm dark:border-gray-600 disabled:opacity-50'
const primary = 'min-h-[40px] rounded bg-blue-600 px-4 text-sm text-white hover:bg-blue-700 disabled:opacity-50'
const link = 'text-sm text-blue-600 hover:underline disabled:opacity-50'
const danger = 'text-sm text-red-600 hover:underline disabled:opacity-50'
const errText = 'mt-1 text-xs text-red-600'
const th = 'px-2 py-2 text-left text-xs font-medium uppercase text-gray-500'
const td = 'px-2 py-1 align-top'
const errMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))

function Err({ errors, path }: { errors: RadiusErrors; path: string }) {
  return errors[path] ? <p className={errText}>{errors[path]}</p> : null
}

function resultBadge(e: AuthEvent) {
  switch (e.result) {
    case 'accept':
    case 'acked':
      return <Badge variant="success">{e.method === 'disconnect' ? t('radius.result.acked') : t('radius.result.accept')}</Badge>
    case 'quarantine':
      return <Badge variant="warning">{t('radius.result.quarantine')}</Badge>
    case 'reject':
      return <Badge variant="error">{t('radius.result.reject')}</Badge>
    case 'refused':
      return <Badge variant="error">{t('radius.result.refused')}</Badge>
    default:
      return <Badge variant="secondary">{t('radius.result.noAnswer')}</Badge>
  }
}

function methodLabel(m: string): string {
  switch (m) {
    case 'mab':
      return t('radius.method.mab')
    case 'peap':
      return t('radius.method.peap')
    case 'eap-tls':
      return t('radius.method.eapTls')
    case 'disconnect':
      return t('radius.method.disconnect')
  }
  return m
}

// Settings: the whole `radius` section, saved with one button.
function Settings({ cfg, canWrite, onSaved }: {
  cfg: Candidate
  canWrite: boolean
  onSaved: () => Promise<void>
}) {
  const [f, setF] = useState<RadiusForm>(() => radiusFormOf(cfg.radius))
  const [errors, setErrors] = useState<RadiusErrors>({})
  const [server, setServer] = useState<ValidationError[]>([])
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [cert, setCert] = useState<ClientCert | null>(null)
  const [certMsg, setCertMsg] = useState<string | null>(null)
  // a new config (after a save) resets the form; compared during render so
  // that nothing resets it after the first render
  const [shown, setShown] = useState(cfg)
  if (cfg !== shown) {
    setShown(cfg)
    setF(radiusFormOf(cfg.radius))
  }

  const vlans = vlanNames(cfg)
  const caOn = !!cfg.internal_ca?.enabled
  const set = (patch: Partial<RadiusForm>) => setF((o) => ({ ...o, ...patch }))
  const setRow = <K extends 'clients' | 'groups' | 'users'>(key: K, i: number, patch: Partial<RadiusForm[K][number]>) =>
    setF((o) => ({ ...o, [key]: o[key].map((r, j) => (j === i ? { ...r, ...patch } : r)) }))
  const removeRow = (key: 'clients' | 'groups' | 'users', i: number) => setF((o) => ({ ...o, [key]: o[key].filter((_, j) => j !== i) }))
  const all: RadiusErrors = { ...errors }
  for (const v of server) if (!all[v.path]) all[v.path] = v.message

  const save = async () => {
    setSaved(false)
    setSaveError(null)
    const e = validateRadius(f, vlans, caOn)
    setErrors(e)
    if (Object.keys(e).length) return
    setBusy(true)
    try {
      const res = await patchSection('radius', buildRadius(f))
      const mine = (res.validation_errors ?? []).filter((v) => v.path.startsWith('radius'))
      setServer(mine)
      if (mine.length === 0) setSaved(true)
      await onSaved()
    } catch (err) {
      setSaveError(errMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const issue = async (user: string) => {
    setCertMsg(null)
    try {
      setCert(await issueCertificate(user, 365))
    } catch (err) {
      setCertMsg(errMessage(err))
    }
  }
  const revoke = async (user: string) => {
    if (!window.confirm(t('radius.revokeConfirm', { user }))) return
    setCertMsg(null)
    try {
      const r = await revokeCertificates(user)
      setCertMsg(t('radius.revoked', { count: r.revoked, user }))
    } catch (err) {
      setCertMsg(errMessage(err))
    }
  }

  const ro = !canWrite
  return (
    <div className="space-y-4">
      <div className={box + ' space-y-3'}>
        <label className="flex items-center gap-2">
          <input type="checkbox" className={check} disabled={ro} checked={f.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          {t('radius.enabled')}
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" className={check} disabled={ro} checked={f.mab} onChange={(e) => set({ mab: e.target.checked })} />
          {t('radius.mab')}
        </label>
        <Err errors={all} path="radius.mab" />
        <label className="flex items-center gap-2">
          <input type="checkbox" className={check} disabled={ro} checked={f.dot1x} onChange={(e) => set({ dot1x: e.target.checked })} />
          {t('radius.dot1x')}
        </label>
        {!caOn && <p className="text-xs text-gray-500">{t('radius.dot1xHint')}</p>}
        <Err errors={all} path="radius.dot1x" />
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block">
            {t('radius.unknown')}
            <select className={inputClass} disabled={ro} value={f.unknown} onChange={(e) => set({ unknown: e.target.value as RadiusForm['unknown'] })}>
              <option value="reject">{t('radius.unknown.reject')}</option>
              <option value="quarantine">{t('radius.unknown.quarantine')}</option>
              <option value="accept">{t('radius.unknown.accept')}</option>
            </select>
          </label>
          {f.unknown === 'quarantine' && (
            <label className="block">
              {t('radius.quarantineVlan')}
              <select className={inputClass} disabled={ro} value={f.quarantineVlan} onChange={(e) => set({ quarantineVlan: e.target.value })}>
                <option value="">{t('radius.pickVlan')}</option>
                {vlans.map((v) => (
                  <option key={v} value={v}>{v}</option>
                ))}
              </select>
              <Err errors={all} path="radius.quarantine_vlan" />
            </label>
          )}
        </div>
      </div>

      {/* Clients */}
      <div className={box}>
        <h3 className="mb-1 font-semibold">{t('radius.clients')}</h3>
        <p className="mb-2 text-xs text-gray-500">{t('radius.clientsHint')}</p>
        <Err errors={all} path="radius.clients" />
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label={t('radius.clients')}>
            <thead>
              <tr>
                <th className={th}>{t('radius.col.name')}</th>
                <th className={th}>{t('radius.col.address')}</th>
                <th className={th}>{t('radius.col.secret')}</th>
                <th className={th}>{t('radius.col.coaPort')}</th>
                <th className={th}>{t('radius.col.description')}</th>
                <th className={th} />
              </tr>
            </thead>
            <tbody>
              {f.clients.map((c, i) => (
                <tr key={i}>
                  <td className={td}>
                    <input aria-label={t('radius.col.name')} className={inputClass} disabled={ro} value={c.name} onChange={(e) => setRow('clients', i, { name: e.target.value })} />
                    <Err errors={all} path={`radius.clients[${i}].name`} />
                  </td>
                  <td className={td}>
                    <input aria-label={t('radius.col.address')} className={inputClass} disabled={ro} value={c.address} placeholder="192.168.1.2" onChange={(e) => setRow('clients', i, { address: e.target.value })} />
                    <Err errors={all} path={`radius.clients[${i}].address`} />
                  </td>
                  <td className={td}>
                    <input
                      aria-label={t('radius.col.secret')}
                      type="password"
                      autoComplete="new-password"
                      className={inputClass}
                      disabled={ro}
                      value={c.secret}
                      placeholder={c.stored ? t('radius.unchanged') : ''}
                      onChange={(e) => setRow('clients', i, { secret: e.target.value })}
                    />
                    <Err errors={all} path={`radius.clients[${i}].secret`} />
                  </td>
                  <td className={td}>
                    <input aria-label={t('radius.col.coaPort')} className={inputClass} disabled={ro} value={c.coaPort} placeholder="3799" inputMode="numeric" onChange={(e) => setRow('clients', i, { coaPort: e.target.value })} />
                    <Err errors={all} path={`radius.clients[${i}].coa_port`} />
                  </td>
                  <td className={td}>
                    <input aria-label={t('radius.col.description')} className={inputClass} disabled={ro} value={c.description} onChange={(e) => setRow('clients', i, { description: e.target.value })} />
                  </td>
                  <td className={td}>
                    {canWrite && (
                      <button type="button" className={danger} onClick={() => removeRow('clients', i)} aria-label={t('radius.removeClient', { name: c.name || '?' })}>
                        {t('radius.remove')}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {canWrite && (
          <button type="button" className={btn + ' mt-2'} onClick={() => set({ clients: [...f.clients, newClient()] })}>
            {t('radius.addClient')}
          </button>
        )}
      </div>

      {/* Groups */}
      <div className={box}>
        <h3 className="mb-1 font-semibold">{t('radius.groups')}</h3>
        <p className="mb-2 text-xs text-gray-500">{t('radius.groupsHint')}</p>
        {f.groups.map((g, i) => (
          <div key={i} className="mb-2 flex flex-wrap items-start gap-2">
            <div>
              <input aria-label={t('radius.col.group')} className={inputClass} disabled={ro} value={g.name} onChange={(e) => setRow('groups', i, { name: e.target.value })} />
              <Err errors={all} path={`radius.groups[${i}].name`} />
            </div>
            <div>
              <select aria-label={t('radius.col.vlan')} className={inputClass} disabled={ro} value={g.vlan} onChange={(e) => setRow('groups', i, { vlan: e.target.value })}>
                <option value="">{t('radius.portVlan')}</option>
                {vlans.map((v) => (
                  <option key={v} value={v}>{v}</option>
                ))}
              </select>
              <Err errors={all} path={`radius.groups[${i}].vlan`} />
            </div>
            {canWrite && (
              <button type="button" className={danger + ' mt-3'} onClick={() => removeRow('groups', i)} aria-label={t('radius.removeGroup', { name: g.name || '?' })}>
                {t('radius.remove')}
              </button>
            )}
          </div>
        ))}
        {canWrite && (
          <button type="button" className={btn} onClick={() => set({ groups: [...f.groups, newGroup()] })}>
            {t('radius.addGroup')}
          </button>
        )}
      </div>

      {/* Users */}
      <div className={box}>
        <h3 className="mb-1 font-semibold">{t('radius.users')}</h3>
        <p className="mb-2 text-xs text-gray-500">{t('radius.usersHint')}</p>
        {certMsg && <p role="status" className="mb-2 text-sm">{certMsg}</p>}
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label={t('radius.users')}>
            <thead>
              <tr>
                <th className={th}>{t('radius.col.user')}</th>
                <th className={th}>{t('radius.col.enabled')}</th>
                <th className={th}>{t('radius.col.password')}</th>
                <th className={th}>{t('radius.col.group')}</th>
                <th className={th} />
              </tr>
            </thead>
            <tbody>
              {f.users.map((u, i) => (
                <tr key={i}>
                  <td className={td}>
                    <input aria-label={t('radius.col.user')} className={inputClass} disabled={ro} value={u.name} onChange={(e) => setRow('users', i, { name: e.target.value })} />
                    <Err errors={all} path={`radius.users[${i}].name`} />
                  </td>
                  <td className={td}>
                    <input aria-label={t('radius.userEnabled', { user: u.name || '?' })} type="checkbox" className={check + ' mt-3'} disabled={ro} checked={u.enabled} onChange={(e) => setRow('users', i, { enabled: e.target.checked })} />
                  </td>
                  <td className={td}>
                    <input
                      aria-label={t('radius.col.password')}
                      type="password"
                      autoComplete="new-password"
                      className={inputClass}
                      disabled={ro}
                      value={u.password}
                      placeholder={u.stored ? t('radius.unchanged') : ''}
                      onChange={(e) => setRow('users', i, { password: e.target.value })}
                    />
                    <Err errors={all} path={`radius.users[${i}].password`} />
                  </td>
                  <td className={td}>
                    <select aria-label={t('radius.col.group')} className={inputClass} disabled={ro} value={u.group} onChange={(e) => setRow('users', i, { group: e.target.value })}>
                      <option value="">{t('radius.noGroup')}</option>
                      {f.groups.filter((g) => g.name.trim()).map((g) => (
                        <option key={g.name} value={g.name.trim()}>{g.name.trim()}</option>
                      ))}
                    </select>
                    <Err errors={all} path={`radius.users[${i}].group`} />
                  </td>
                  <td className={td + ' space-x-3 whitespace-nowrap'}>
                    {canWrite && u.stored && f.dot1x && caOn && (
                      <>
                        <button type="button" className={link} onClick={() => issue(u.name)}>{t('radius.issueCert')}</button>
                        <button type="button" className={danger} onClick={() => revoke(u.name)}>{t('radius.revokeCerts')}</button>
                      </>
                    )}
                    {canWrite && (
                      <button type="button" className={danger} onClick={() => removeRow('users', i)} aria-label={t('radius.removeUser', { user: u.name || '?' })}>
                        {t('radius.remove')}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {canWrite && (
          <button type="button" className={btn + ' mt-2'} onClick={() => set({ users: [...f.users, newUser()] })}>
            {t('radius.addUser')}
          </button>
        )}
      </div>

      {saved && (
        <div role="status" className="rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/30 dark:text-green-200">
          {t('radius.saved')}
        </div>
      )}
      {saveError && (
        <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">{saveError}</div>
      )}
      {Object.keys(errors).length > 0 && <p role="alert" className={errText}>{t('radius.fixErrors')}</p>}
      {canWrite && (
        <button type="button" className={primary} disabled={busy} onClick={save}>
          {t('radius.save')}
        </button>
      )}

      <Dialog open={!!cert} onClose={() => setCert(null)} title={t('radius.certTitle', { user: cert?.user ?? '' })} size="medium">
        {cert && (
          <div className="space-y-3 text-sm">
            <p className="rounded bg-yellow-50 p-2 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">{t('radius.certOnce')}</p>
            <p>{t('radius.certValid', { date: new Date(cert.not_after).toLocaleDateString() })}</p>
            <div className="flex flex-wrap gap-3">
              <a className={link} href={pemDownload(cert.certificate)} download={`${cert.user}.pem`}>{t('radius.downloadCert')}</a>
              <a className={link} href={pemDownload(cert.key)} download={`${cert.user}.key`}>{t('radius.downloadKey')}</a>
              <a className={link} href={pemDownload(cert.ca)} download="goblin-gate-ca.pem">{t('radius.downloadCA')}</a>
            </div>
            <p className="text-xs text-gray-500">{t('radius.caFingerprint', { sha256: cert.ca_sha256 })}</p>
            <p className="text-xs text-gray-500">{t('radius.p12Hint')}</p>
            <div className="flex justify-end">
              <button type="button" className={btn} onClick={() => setCert(null)}>{t('radius.close')}</button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

// WifiPasswords: per-device Wi-Fi passwords (PPSK), saved at once.
function WifiPasswords() {
  const [list, setList] = useState<PPSKEntry[]>([])
  const [mac, setMac] = useState('')
  const [psk, setPsk] = useState('')
  const [shown, setShown] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback(() => {
    getPPSKs()
      .then((r) => setList(r.devices))
      .catch((e) => setError(errMessage(e)))
  }, [])
  useEffect(load, [load])
  const add = async () => {
    setError(null)
    if (psk.length < 8 || psk.length > 63) {
      setError(t('radius.errPsk'))
      return
    }
    try {
      await setPPSK(mac.trim(), psk)
      setMac('')
      setPsk('')
      load()
    } catch (e) {
      setError(errMessage(e))
    }
  }
  const remove = async (m: string) => {
    setError(null)
    try {
      await deletePPSK(m)
      load()
    } catch (e) {
      setError(errMessage(e))
    }
  }
  return (
    <div className={box}>
      <h3 className="mb-1 font-semibold">{t('radius.ppsk')}</h3>
      <p className="mb-2 text-xs text-gray-500">{t('radius.ppskHint')}</p>
      {error && <p role="alert" className={errText}>{error}</p>}
      {list.length === 0 && <p className="text-gray-500">{t('radius.noPpsk')}</p>}
      {list.length > 0 && (
        <table className="mb-2 min-w-full text-sm" aria-label={t('radius.ppsk')}>
          <thead>
            <tr>
              <th className={th}>{t('radius.col.device')}</th>
              <th className={th}>{t('radius.col.wifiPassword')}</th>
              <th className={th} />
            </tr>
          </thead>
          <tbody>
            {list.map((e) => (
              <tr key={e.mac}>
                <td className={td + ' font-mono text-xs'}>{colonMAC(e.mac)}</td>
                <td className={td + ' font-mono text-xs'}>
                  {shown === e.mac ? e.psk : '••••••••'}{' '}
                  <button type="button" className={link} onClick={() => setShown(shown === e.mac ? null : e.mac)}>
                    {shown === e.mac ? t('radius.hide') : t('radius.show')}
                  </button>
                </td>
                <td className={td}>
                  <button type="button" className={danger} onClick={() => remove(e.mac)} aria-label={t('radius.removePpsk', { mac: colonMAC(e.mac) })}>
                    {t('radius.remove')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <label className="block">
          {t('radius.col.device')}
          <input className={inputClass} value={mac} placeholder="aa:bb:cc:dd:ee:ff" onChange={(e) => setMac(e.target.value)} />
        </label>
        <label className="block">
          {t('radius.col.wifiPassword')}
          <input className={inputClass} value={psk} onChange={(e) => setPsk(e.target.value)} />
        </label>
        <button type="button" className={btn} disabled={!mac.trim() || !psk} onClick={add}>
          {t('radius.setPpsk')}
        </button>
      </div>
    </div>
  )
}

// Authentications: the recent log, filterable, refreshed every 10 s.
export function Authentications() {
  const [events, setEvents] = useState<AuthEvent[]>([])
  const [error, setError] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [result, setResult] = useState<ResultFilter>('')
  useEffect(() => {
    let live = true
    const load = () =>
      getRadiusLog(500)
        .then((r) => live && (setEvents(r.events), setError(null)))
        .catch((e) => live && setError(errMessage(e)))
    load()
    const id = setInterval(load, 10000)
    return () => {
      live = false
      clearInterval(id)
    }
  }, [])
  const shown = filterEvents(events, text, result)
  return (
    <div className={box}>
      <h3 className="mb-2 font-semibold">{t('radius.log')}</h3>
      <div className="mb-2 flex flex-wrap gap-2">
        <input
          aria-label={t('radius.filter')}
          placeholder={t('radius.filterPlaceholder')}
          className={inputClass + ' sm:w-64'}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
        <select aria-label={t('radius.filterResult')} className={inputClass + ' sm:w-48'} value={result} onChange={(e) => setResult(e.target.value as ResultFilter)}>
          <option value="">{t('radius.allResults')}</option>
          <option value="accept">{t('radius.result.accept')}</option>
          <option value="quarantine">{t('radius.result.quarantine')}</option>
          <option value="reject">{t('radius.result.reject')}</option>
          <option value="disconnect">{t('radius.method.disconnect')}</option>
        </select>
      </div>
      {error && <p role="alert" className={errText}>{error}</p>}
      <div className="overflow-x-auto">
        <table className="min-w-full text-sm" aria-label={t('radius.log')}>
          <thead>
            <tr>
              <th className={th}>{t('radius.col.time')}</th>
              <th className={th}>{t('radius.col.result')}</th>
              <th className={th}>{t('radius.col.device')}</th>
              <th className={th}>{t('radius.col.user')}</th>
              <th className={th}>{t('radius.col.method')}</th>
              <th className={th}>{t('radius.col.where')}</th>
              <th className={th}>{t('radius.col.vlan')}</th>
              <th className={th}>{t('radius.col.reason')}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((e, i) => (
              <tr key={`${e.time}-${i}`} className="border-t border-gray-100 dark:border-gray-700">
                <td className={td + ' whitespace-nowrap'}>{new Date(e.time).toLocaleString()}</td>
                <td className={td}>{resultBadge(e)}</td>
                <td className={td + ' font-mono text-xs'}>{colonMAC(e.mac) || '—'}</td>
                <td className={td}>{e.user || '—'}</td>
                <td className={td}>{methodLabel(e.method)}</td>
                <td className={td}>{e.port ? t('radius.where', { client: e.client || e.nas || '?', port: e.port }) : e.client || e.nas || '—'}</td>
                <td className={td}>{e.vlan ? t('radius.vlanCell', { vlan: e.vlan, id: e.vlan_id ?? '' }) : '—'}</td>
                <td className={td}>{e.reason || '—'}</td>
              </tr>
            ))}
            {shown.length === 0 && (
              <tr>
                <td colSpan={8} className="px-2 py-4 text-center text-gray-500">{t('radius.noEvents')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// Sessions: the devices let in now, with a Disconnect button.
function Sessions({ canAct }: { canAct: boolean }) {
  const [list, setList] = useState<RadiusSession[]>([])
  const [msg, setMsg] = useState<string | null>(null)
  const load = useCallback(() => {
    getRadiusSessions()
      .then((r) => setList(r.sessions))
      .catch((e) => setMsg(errMessage(e)))
  }, [])
  useEffect(() => {
    load()
    const id = setInterval(load, 10000)
    return () => clearInterval(id)
  }, [load])
  const disconnect = async (s: RadiusSession) => {
    setMsg(null)
    try {
      const r = await disconnectSession(s.mac)
      setMsg(r.result === 'acked' ? t('radius.disconnected', { mac: colonMAC(s.mac) }) : t('radius.disconnectFailed', { mac: colonMAC(s.mac), result: r.result }))
      load()
    } catch (e) {
      setMsg(errMessage(e))
    }
  }
  return (
    <div className={box}>
      <h3 className="mb-2 font-semibold">{t('radius.sessions')}</h3>
      {msg && <p role="status" className="mb-2">{msg}</p>}
      {list.length === 0 && <p className="text-gray-500">{t('radius.noSessions')}</p>}
      {list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full text-sm" aria-label={t('radius.sessions')}>
            <thead>
              <tr>
                <th className={th}>{t('radius.col.device')}</th>
                <th className={th}>{t('radius.col.user')}</th>
                <th className={th}>{t('radius.col.where')}</th>
                <th className={th}>{t('radius.col.vlan')}</th>
                <th className={th}>{t('radius.col.since')}</th>
                <th className={th} />
              </tr>
            </thead>
            <tbody>
              {list.map((s) => (
                <tr key={s.mac} className="border-t border-gray-100 dark:border-gray-700">
                  <td className={td + ' font-mono text-xs'}>{colonMAC(s.mac)}</td>
                  <td className={td}>{s.user || '—'}</td>
                  <td className={td}>
                    {s.ssid
                      ? t('radius.whereWifi', { ap: s.nas_identifier || s.client || s.nas, ssid: s.ssid })
                      : s.port
                        ? t('radius.where', { client: s.nas_identifier || s.client || s.nas, port: s.port })
                        : s.nas_identifier || s.client || s.nas}
                  </td>
                  <td className={td}>{s.vlan || '—'}</td>
                  <td className={td + ' whitespace-nowrap'}>{new Date(s.started).toLocaleString()}</td>
                  <td className={td}>
                    {canAct && (
                      <button type="button" className={link} onClick={() => disconnect(s)} aria-label={t('radius.disconnectDevice', { mac: colonMAC(s.mac) })}>
                        {t('radius.disconnect')}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

const RadiusPage: React.FC = () => {
  const perms = usePermissions()
  const canWrite = perms.can('write:network')
  const canAct = perms.can('actions')
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
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

  if (loadError) return <div className="text-red-600">{t('radius.loadFailed', { message: loadError })}</div>
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-bold text-gray-800 dark:text-white">{t('radius.title')}</h2>
        <p className="text-sm text-gray-500">
          {t('radius.intro')}{' '}
          <a className="text-blue-600 underline" href={radiusWikiURL} target="_blank" rel="noreferrer">
            {t('radius.wiki')}
          </a>
        </p>
      </div>
      {perms.loaded && !canWrite && <p className="text-sm text-gray-500">{t('radius.readOnly')}</p>}
      {cfg && <Settings cfg={cfg} canWrite={canWrite} onSaved={load} />}
      {perms.loaded && canWrite && <WifiPasswords />}
      <Sessions canAct={canAct} />
      <Authentications />
    </div>
  )
}

export default RadiusPage
