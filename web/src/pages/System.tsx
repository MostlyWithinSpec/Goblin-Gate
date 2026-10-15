import React, { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Download, Upload, Plus, Trash2, KeyRound } from 'lucide-react'
import { getCandidate, patchSection, type Candidate, type SystemSettings, type SSHKey } from '../api/objects'
import { inputClass, NAME_RE, omitEmpty } from '../components/ConfigList'
import { getConfigDiff, type DiffResponse } from '../api/config'
import { getAdmins, createAdmin, updateAdmin, deleteAdmin, getTokens, createToken, deleteToken, downloadBackup, restoreBackup, getSystemInfo, type AdminInfo, type TokenInfo, type SystemInfo } from '../api/system'
import { getMe, type Me } from '../api/auth'

const TABS = ['General', 'Admins', 'API Tokens', 'Backup', 'About'] as const
type Tab = (typeof TABS)[number]

// Common timezones for the select; free-text fallback via datalist.
const TIMEZONES = [
  'UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'Europe/London', 'Europe/Berlin', 'Europe/Paris', 'Europe/Madrid', 'Europe/Rome',
  'Europe/Amsterdam', 'Europe/Stockholm', 'Europe/Warsaw', 'Asia/Tokyo', 'Asia/Shanghai',
  'Asia/Singapore', 'Asia/Kolkata', 'Australia/Sydney', 'Pacific/Auckland',
]

const System: React.FC = () => {
  const [tab, setTab] = useState<Tab>('General')

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-gray-800 dark:text-white mb-4">System Configuration</h1>
      <div className="mb-4 flex gap-1 border-b border-gray-200 dark:border-gray-700">
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium rounded-t ${tab === t ? 'border border-b-0 border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 text-blue-600' : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300'}`}
          >
            {t}
          </button>
        ))}
      </div>
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6">
        {tab === 'General' && <GeneralTab />}
        {tab === 'Admins' && <AdminsTab />}
        {tab === 'API Tokens' && <TokensTab />}
        {tab === 'Backup' && <BackupTab />}
        {tab === 'About' && <AboutTab />}
      </div>
      <div className="mt-4 flex gap-3">
        <Link to="/system/revisions" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Revisions</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">View and manage configuration history</span>
        </Link>
        <Link to="/system/notifications" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Notifications</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Channels and per-event routing</span>
        </Link>
      </div>
    </div>
  )
}

// --- General ------------------------------------------------------------------

const GeneralTab: React.FC = () => {
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [form, setForm] = useState<SystemSettings>({ hostname: '', timezone: 'UTC' })

  useEffect(() => {
    getCandidate().then((c) => {
      setCfg(c)
      if (c.system) setForm({ hostname: c.system.hostname ?? '', domain: c.system.domain, timezone: c.system.timezone || 'UTC', admin_port: c.system.admin_port, ntp_servers: c.system.ntp_servers, dns_servers: c.system.dns_servers })
    }).catch((e: Error) => setError(e.message))
  }, [])

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setSaved(false)
    const value = omitEmpty({ ...form, hostname: form.hostname.trim(), domain: form.domain?.trim() || undefined })
    try {
      // keep the fields this form does not edit (SSH keys, update channel, ...)
      const keep: Partial<SystemSettings> = { ...(await getCandidate()).system }
      for (const k of ['domain', 'admin_port', 'ntp_servers', 'dns_servers'] as const) delete keep[k] // cleared here = removed
      await patchSection('system', { ...keep, ...value })
      setSaved(true)
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (!cfg) return <p className="text-gray-500">{error ?? 'Loading…'}</p>

  const set = (patch: Partial<SystemSettings>) => setForm((f) => ({ ...f, ...patch }))

  return (
    <form onSubmit={save} className="max-w-xl space-y-4">
      {saved && <div className="rounded bg-green-50 p-2 text-sm text-green-700" role="status">Saved. Use the commit bar to apply.</div>}
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      <label className="block text-sm">
        Hostname
        <input aria-label="Hostname" className={inputClass} value={form.hostname} onChange={(e) => set({ hostname: e.target.value })} />
      </label>
      <label className="block text-sm">
        Domain
        <input aria-label="Domain" className={inputClass} value={form.domain ?? ''} onChange={(e) => set({ domain: e.target.value || undefined })} placeholder="(optional)" />
      </label>
      <label className="block text-sm">
        Time zone
        <select aria-label="Time zone" className={inputClass} value={form.timezone} onChange={(e) => set({ timezone: e.target.value })}>
          {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
          {!TIMEZONES.includes(form.timezone) && <option value={form.timezone}>{form.timezone}</option>}
        </select>
      </label>
      <label className="block text-sm">
        NTP servers
        <textarea aria-label="NTP servers" rows={3} className={inputClass + ' font-mono text-xs'} value={(form.ntp_servers ?? []).join('\n')} onChange={(e) => set({ ntp_servers: e.target.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean) })} placeholder="pool.ntp.org" />
      </label>
      <label className="block text-sm">
        DNS servers
        <textarea aria-label="DNS servers" rows={3} className={inputClass + ' font-mono text-xs'} value={(form.dns_servers ?? []).join('\n')} onChange={(e) => set({ dns_servers: e.target.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean) })} placeholder="1.1.1.1" />
      </label>
      <button type="submit" className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700">Save</button>
    </form>
  )
}

// --- Admins -------------------------------------------------------------------

const AdminsTab: React.FC = () => {
  const [admins, setAdmins] = useState<AdminInfo[] | null>(null)
  const [me, setMe] = useState<Me | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [newAdmin, setNewAdmin] = useState({ username: '', role: 'admin', password: '' })
  const [formErrors, setFormErrors] = useState<Record<string, string>>({})

  const load = useCallback(() => {
    getAdmins().then(setAdmins).catch((e: Error) => setError(e.message))
    getMe().then(setMe)
  }, [])
  useEffect(load, [load])

  const submitAdd = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const errs: Record<string, string> = {}
    if (!NAME_RE.test(newAdmin.username)) errs.username = 'Use 1-63 letters, digits, ".", "_" or "-"'
    else if (admins?.some((a) => a.username === newAdmin.username)) errs.username = 'Name already used'
    if (newAdmin.role !== 'admin' && newAdmin.role !== 'readonly') errs.role = 'Must be admin or readonly'
    if (!newAdmin.password) errs.password = 'Password is required'
    setFormErrors(errs)
    if (Object.keys(errs).length) return
    try {
      await createAdmin(newAdmin)
      setShowAdd(false)
      setNewAdmin({ username: '', role: 'admin', password: '' })
      load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const changeRole = async (name: string, role: string) => {
    try { await updateAdmin(name, { role }); load() } catch (e) { setError((e as Error).message) }
  }

  const resetPassword = async (name: string) => {
    const pw = window.prompt(`New password for ${name}:`)
    if (!pw) return
    try { await updateAdmin(name, { password: pw }); load() } catch (e) { setError((e as Error).message) }
  }

  // Last admin with a password cannot be deleted (backend enforces this too).
  const canDelete = (a: AdminInfo) => {
    if (me?.user === a.username) return false
    const othersWithPw = admins?.filter((x) => x.has_password && x.role === 'admin' && x.username !== a.username) ?? []
    if (a.role === 'admin' && a.has_password && othersWithPw.length === 0) return false
    return true
  }

  const doDelete = async (name: string) => {
    try { await deleteAdmin(name); load() } catch (e) { setError((e as Error).message) }
  }

  if (!admins) return <p className="text-gray-500">{error ?? 'Loading…'}</p>

  return (
    <div>
      {error && <div className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Administrators</h2>
        <button type="button" onClick={() => setShowAdd(true)} className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">
          <Plus size={16} /> Add admin
        </button>
      </div>
      <table className="min-w-full text-sm">
        <thead className="text-left text-xs uppercase text-gray-500">
          <tr><th className="px-3 py-2">Username</th><th className="px-3 py-2">Role</th><th className="px-3 py-2"></th></tr>
        </thead>
        <tbody>
          {admins.map((a) => (
            <tr key={a.username} className="border-t border-gray-100 dark:border-gray-700">
              <td className="px-3 py-2 font-medium">{a.username}{me?.user === a.username && <span className="ml-1 text-xs text-gray-400">(you)</span>}</td>
              <td className="px-3 py-2">
                <select aria-label={`Role for ${a.username}`} className="rounded border border-gray-300 px-1 py-0.5 text-sm dark:bg-gray-700" value={a.role} onChange={(e) => changeRole(a.username, e.target.value)}>
                  <option value="admin">admin</option>
                  <option value="readonly">readonly</option>
                </select>
              </td>
              <td className="whitespace-nowrap px-3 py-2 text-right">
                <button type="button" onClick={() => resetPassword(a.username)} className="mr-2 text-sm text-blue-600 hover:underline">Reset password</button>
                <button type="button" disabled={!canDelete(a)} onClick={() => doDelete(a.username)} className="text-red-600 disabled:opacity-40" aria-label={`Delete ${a.username}`}>
                  <Trash2 size={16} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {showAdd && (
        <form onSubmit={submitAdd} className="mt-4 max-w-sm space-y-3 rounded border border-gray-200 p-4 dark:border-gray-600">
          <h3 className="text-sm font-semibold text-gray-800 dark:text-white">New admin</h3>
          <label className="block text-sm">
            Username
            <input aria-label="Username" className={inputClass} value={newAdmin.username} onChange={(e) => setNewAdmin((s) => ({ ...s, username: e.target.value }))} />
            {formErrors.username && <p className="mt-1 text-xs text-red-600">{formErrors.username}</p>}
          </label>
          <label className="block text-sm">
            Role
            <select aria-label="Role" className={inputClass} value={newAdmin.role} onChange={(e) => setNewAdmin((s) => ({ ...s, role: e.target.value }))}>
              <option value="admin">admin</option>
              <option value="readonly">readonly</option>
            </select>
            {formErrors.role && <p className="mt-1 text-xs text-red-600">{formErrors.role}</p>}
          </label>
          <label className="block text-sm">
            Password
            <input aria-label="Password" type="password" className={inputClass} value={newAdmin.password} onChange={(e) => setNewAdmin((s) => ({ ...s, password: e.target.value }))} />
            {formErrors.password && <p className="mt-1 text-xs text-red-600">{formErrors.password}</p>}
          </label>
          <div className="flex gap-2">
            <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-sm text-white">Create</button>
            <button type="button" onClick={() => { setShowAdd(false); setFormErrors({}) }} className="rounded border border-gray-300 px-3 py-1 text-sm">Cancel</button>
          </div>
        </form>
      )}

      <SSHKeysSection />
    </div>
  )
}

// SSH keys are stored in candidate system.ssh_keys and saved with patchSection.
const SSHKeysSection: React.FC = () => {
  const [keys, setKeys] = useState<SSHKey[]>([])
  const [loaded, setLoaded] = useState(false)
  const [name, setName] = useState('')
  const [key, setKey] = useState('')

  useEffect(() => {
    getCandidate().then((c) => { setKeys(c.system?.ssh_keys ?? []); setLoaded(true) }).catch(() => setLoaded(true))
  }, [])

  const saveKeys = async (next: SSHKey[]) => {
    try { await patchSection('system.ssh_keys', next); setKeys(next) } catch { /* ignore */ }
  }

  const add = () => {
    if (!name.trim() || !key.trim()) return
    saveKeys([...keys, { name: name.trim(), key: key.trim() }])
    setName(''); setKey('')
  }

  if (!loaded) return null

  return (
    <div className="mt-6">
      <h3 className="mb-2 flex items-center gap-1 text-sm font-semibold text-gray-800 dark:text-white"><KeyRound size={14} /> SSH keys</h3>
      {keys.length > 0 && (
        <ul className="mb-2 space-y-1">
          {keys.map((k) => (
            <li key={k.name} className="flex items-center justify-between rounded bg-gray-50 px-2 py-1 text-xs dark:bg-gray-700/40">
              <span className="font-mono">{k.name}</span>
              <button type="button" onClick={() => saveKeys(keys.filter((x) => x.name !== k.name))} className="text-red-600" aria-label={`Remove key ${k.name}`}><Trash2 size={14} /></button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex gap-2">
        <input aria-label="SSH key name" className={inputClass + ' !mt-0'} placeholder="key name" value={name} onChange={(e) => setName(e.target.value)} />
        <input aria-label="SSH public key" className={inputClass + ' !mt-0 flex-1 font-mono text-xs'} placeholder="ssh-ed25519 AAAA..." value={key} onChange={(e) => setKey(e.target.value)} />
        <button type="button" onClick={add} className="rounded bg-blue-600 px-3 py-1 text-sm text-white">Add</button>
      </div>
    </div>
  )
}

// --- API Tokens ---------------------------------------------------------------

const TokensTab: React.FC = () => {
  const [tokens, setTokens] = useState<TokenInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [readonly, setReadonly] = useState(false)
  const [created, setCreated] = useState<string | null>(null)

  const load = useCallback(() => { getTokens().then(setTokens).catch((e: Error) => setError(e.message)) }, [])
  useEffect(load, [load])

  const create = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!name.trim()) return
    try {
      const res = await createToken({ name: name.trim(), readonly })
      setCreated(res.token)
      setName(''); setReadonly(false)
      load()
    } catch (e) { setError((e as Error).message) }
  }

  const doDelete = async (id: string) => {
    try { await deleteToken(id); load() } catch (e) { setError((e as Error).message) }
  }

  if (!tokens) return <p className="text-gray-500">{error ?? 'Loading…'}</p>

  return (
    <div>
      {error && <div className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {created && (
        <div className="mb-4 rounded bg-green-50 p-3 text-sm" role="status">
          <p className="font-medium text-green-800">Token created — copy it now, it will not be shown again:</p>
          <code className="mt-1 block break-all rounded bg-white p-2 font-mono text-xs dark:bg-gray-900">{created}</code>
        </div>
      )}
      <form onSubmit={create} className="mb-4 flex items-end gap-2">
        <label className="block text-sm">
          Name
          <input aria-label="Token name" className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-deploy" />
        </label>
        <label className="flex items-center gap-1 pb-2 text-sm">
          <input type="checkbox" checked={readonly} onChange={(e) => setReadonly(e.target.checked)} /> readonly
        </label>
        <button type="submit" className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">Create</button>
      </form>
      {tokens.length === 0 ? (
        <p className="text-sm text-gray-500">No tokens yet.</p>
      ) : (
        <table className="min-w-full text-sm">
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr><th className="px-3 py-2">Name</th><th className="px-3 py-2">User</th><th className="px-3 py-2">Role</th><th className="px-3 py-2">Created</th><th className="px-3 py-2">Last used</th><th></th></tr>
          </thead>
          <tbody>
            {tokens.map((t) => (
              <tr key={t.id} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-2">{t.name}</td>
                <td className="px-3 py-2">{t.user}</td>
                <td className="px-3 py-2">{t.role}</td>
                <td className="px-3 py-2 text-xs">{new Date(t.created).toLocaleString()}</td>
                <td className="px-3 py-2 text-xs">{t.last_used ? new Date(t.last_used).toLocaleString() : '—'}</td>
                <td className="px-3 py-2 text-right">
                  <button type="button" onClick={() => doDelete(t.id)} className="text-red-600" aria-label={`Delete token ${t.name}`}><Trash2 size={16} /></button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// --- Backup -------------------------------------------------------------------

const BackupTab: React.FC = () => {
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [diff, setDiff] = useState<DiffResponse | null>(null)
  const [busy, setBusy] = useState(false)

  const doDownload = async () => {
    setBusy(true); setError(null)
    try { await downloadBackup() } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }

  const doRestore = async (ev: React.ChangeEvent<HTMLInputElement>) => {
    const file = ev.target.files?.[0]
    if (!file) return
    setBusy(true); setError(null); setMessage(null); setDiff(null)
    try {
      await restoreBackup(file)
      setMessage('Backup loaded into candidate. Review the diff below, then commit.')
      const d = await getConfigDiff()
      setDiff(d)
    } catch (e) {
      setError((e as Error).message)
    } finally { setBusy(false); ev.target.value = '' }
  }

  return (
    <div className="max-w-2xl space-y-4">
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {message && <div className="rounded bg-green-50 p-2 text-sm text-green-700" role="status">{message}</div>}

      <div className="flex gap-3">
        <button type="button" onClick={doDownload} disabled={busy} className="flex items-center gap-1 rounded bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
          <Download size={16} /> Download backup
        </button>
        <label className="flex cursor-pointer items-center gap-1 rounded border border-gray-300 px-4 py-2 text-sm dark:border-gray-600">
          <Upload size={16} /> Restore from file
          <input type="file" accept=".zip" className="hidden" onChange={doRestore} disabled={busy} />
        </label>
      </div>

      {diff && (
        <div className="rounded border border-gray-200 p-3 dark:border-gray-600">
          <h3 className="mb-2 text-sm font-semibold text-gray-800 dark:text-white">Candidate diff ({diff.count} change{diff.count !== 1 ? 's' : ''})</h3>
          {diff.count === 0 ? (
            <p className="text-sm text-gray-500">No changes.</p>
          ) : (
            <ul className="max-h-64 space-y-1 overflow-y-auto text-xs">
              {diff.changes.map((c, i) => (
                <li key={i} className="rounded bg-gray-50 px-2 py-1 font-mono dark:bg-gray-700/40">
                  <span className="text-gray-500">{c.path}</span>
                  {c.old !== undefined && <span className="ml-2 text-red-600 line-through">{JSON.stringify(c.old)}</span>}
                  {c.new !== undefined && <span className="ml-2 text-green-700 dark:text-green-400">{JSON.stringify(c.new)}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

// --- About --------------------------------------------------------------------

const formatUptime = (s: number): string => {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

const AboutTab: React.FC = () => {
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { getSystemInfo().then(setInfo).catch((e: Error) => setError(e.message)) }, [])

  if (error) return <p className="text-sm text-red-600">{error}</p>
  if (!info) return <p className="text-gray-500">Loading…</p>

  const rows: [string, React.ReactNode][] = [
    ['Version', info.version],
    ['Time', new Date(info.time).toLocaleString()],
    ...(info.hostname ? [['Hostname', info.hostname] as [string, React.ReactNode]] : []),
    ...(info.kernel ? [['Kernel', info.kernel] as [string, React.ReactNode]] : []),
    ...(info.uptime_seconds !== undefined ? [['Uptime', formatUptime(info.uptime_seconds)] as [string, React.ReactNode]] : []),
    ...(info.timezone ? [['Time zone', info.timezone] as [string, React.ReactNode]] : []),
  ]

  return (
    <div className="max-w-md space-y-3">
      <table className="text-sm">
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k} className="border-t border-gray-100 dark:border-gray-700">
              <td className="px-2 py-1.5 font-medium text-gray-600 dark:text-gray-400">{k}</td>
              <td className="px-2 py-1.5">{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {info.ntp_synchronized !== undefined && (
        <span className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${info.ntp_synchronized ? 'bg-green-100 text-green-800' : 'bg-yellow-100 text-yellow-800'}`}>
          NTP {info.ntp_synchronized ? 'synchronized' : 'not synchronized'}
        </span>
      )}
    </div>
  )
}

export default System
