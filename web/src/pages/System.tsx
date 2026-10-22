import React, { useCallback, useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Download, Upload, Plus, Trash2, KeyRound, FileUp, AlertTriangle, LogOut, GitBranch, Copy, Check, RefreshCw, Info } from 'lucide-react'
import { getCandidate, patchSection, getDevices, type Candidate, type SystemSettings, type SSHKey, type Device, type MetricsSettings } from '../api/objects'
import { inputClass, NAME_RE, omitEmpty } from '../components/ConfigList'
import { getConfigDiff, type DiffResponse } from '../api/config'
import { getAdmins, createAdmin, updateAdmin, deleteAdmin, getTokens, createToken, deleteToken, downloadBackup, restoreBackup, importPfSense, getSystemInfo, getSessionList, deleteSession, type AdminInfo, type TokenInfo, type SystemInfo, type ImportResult, type ImportReportEntry, type SessionInfo } from '../api/system'
import { getMe, setup2FA, enable2FA, disable2FA, type Me, type TwoFactorSetup } from '../api/auth'
import { getGitBackupStatus, pushGitBackup, forgetGitBackupHostKey, type GitBackupStatus } from '../api/gitbackup'
import { Badge } from '../components/Badge'

const TABS = ['General', 'Admins', 'API Tokens', 'Backup', 'Import', 'About'] as const
type Tab = (typeof TABS)[number]

// Common timezones for the select; free-text fallback via datalist.
const TIMEZONES = [
  'UTC', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'Europe/London', 'Europe/Berlin', 'Europe/Paris', 'Europe/Madrid', 'Europe/Rome',
  'Europe/Amsterdam', 'Europe/Stockholm', 'Europe/Warsaw', 'Asia/Tokyo', 'Asia/Shanghai',
  'Asia/Singapore', 'Asia/Kolkata', 'Australia/Sydney', 'Pacific/Auckland',
]

const System: React.FC = () => {
  // A Goblin Checks finding links here as /system?tab=Admins, so the tab it
  // names opens straight away; an unknown or missing ?tab= opens General.
  const [search] = useSearchParams()
  const tabParam = search.get('tab')
  const [tab, setTab] = useState<Tab>(TABS.includes(tabParam as Tab) ? (tabParam as Tab) : 'General')

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-gray-800 dark:text-white mb-4">System Configuration</h1>
      <div className="mb-4 flex gap-1 border-b border-gray-200 dark:border-gray-700">
        {TABS.map((t) => (
          <button
            key={t}
            type="button"
            aria-current={tab === t ? 'page' : undefined}
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
        {tab === 'Import' && <ImportTab />}
        {tab === 'About' && <AboutTab />}
      </div>
      <div className="mt-4 flex flex-wrap gap-3">
        <Link to="/system/revisions" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Revisions</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">View and manage configuration history</span>
        </Link>
        <Link to="/system/notifications" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Notifications</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Channels and per-event routing</span>
        </Link>
        <Link to="/system/plugins" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Plugins</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Installed plugins, capabilities and settings</span>
        </Link>
        <Link to="/system/updates" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Updates</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Channel, available version and update history</span>
        </Link>
        <Link to="/system/generated" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Generated config</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Renderer output: running vs candidate, with diff</span>
        </Link>
        <Link to="/system/diagnostics" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">Diagnostics</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Ping, traceroute and DNS lookup from the box</span>
        </Link>
        <Link to="/system/ups" className="inline-block p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg hover:bg-blue-100 dark:hover:bg-blue-900/30 transition-colors text-sm min-h-[40px] flex items-center justify-center">
          <span className="font-semibold text-blue-700 dark:text-blue-300">UPS</span>
          <span className="ml-2 text-gray-600 dark:text-gray-300">Power state, battery runtime and clean shutdown</span>
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
    <>
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
    <MetricsSection initial={cfg.system?.metrics} />
    </>
  )
}

// Prometheus metrics: saved on its own to candidate system.metrics.
const MetricsSection: React.FC<{ initial?: MetricsSettings }> = ({ initial }) => {
  const [enabled, setEnabled] = useState(initial?.enabled ?? false)
  const [allow, setAllow] = useState((initial?.allow ?? []).join('\n'))
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setSaved(false)
    const list = allow.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean)
    try {
      await patchSection('system.metrics', list.length > 0 ? { enabled, allow: list } : { enabled })
      setSaved(true)
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <form onSubmit={save} className="mt-8 max-w-xl space-y-3 border-t border-gray-200 pt-6 dark:border-gray-700">
      <h3 className="text-sm font-semibold text-gray-800 dark:text-white">Metrics</h3>
      <p className="text-xs text-gray-500 dark:text-gray-400">
        Prometheus metrics at <code>/metrics</code> on the admin port. Scrapers from the allow list need nothing else; others need an API token (System → API Tokens, read-only is enough).
      </p>
      {saved && <div className="rounded bg-green-50 p-2 text-sm text-green-700" role="status">Saved. Use the commit bar to apply.</div>}
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" aria-label="Enable metrics" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Enable /metrics
      </label>
      <label className="block text-sm">
        Allowed without a token (one CIDR per line)
        <textarea aria-label="Metrics allow list" rows={3} className={inputClass + ' font-mono text-xs'} value={allow} onChange={(e) => setAllow(e.target.value)} placeholder="192.168.1.10/32" />
      </label>
      <button type="submit" className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700">Save metrics</button>
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
          <tr><th className="px-3 py-2">Username</th><th className="px-3 py-2">Role</th><th className="px-3 py-2">2FA</th><th className="px-3 py-2"></th></tr>
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
              <td className="px-3 py-2">{a.totp_enabled && <Badge variant="success">2FA</Badge>}</td>
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

      <TwoFactorCard me={me} onChanged={() => getMe().then(setMe)} />
      <SessionsCard />
      <SSHKeysSection />
    </div>
  )
}

// Two-factor (TOTP) login for the signed-in user (task 085 backend).
const TwoFactorCard: React.FC<{ me: Me | null; onChanged: () => void }> = ({ me, onChanged }) => {
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null)
  const [code, setCode] = useState('')
  const [offPassword, setOffPassword] = useState('')
  const [showOff, setShowOff] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  const startSetup = async () => {
    setBusy(true); setError(null)
    try {
      const s = await setup2FA()
      setSetup(s)
      setCode('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const turnOn = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!code) return
    setBusy(true); setError(null)
    try {
      await enable2FA(code)
      setSetup(null)
      setCode('')
      setDone(true)
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const turnOff = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!offPassword) return
    setBusy(true); setError(null)
    try {
      await disable2FA(offPassword)
      setShowOff(false)
      setOffPassword('')
      setDone(true)
      onChanged()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const copySecret = () => {
    if (!setup) return
    navigator.clipboard?.writeText(setup.secret).catch(() => {})
  }

  return (
    <div className="mt-6 rounded-lg border border-gray-200 p-4 dark:border-gray-600">
      <h3 className="text-sm font-semibold text-gray-800 dark:text-white">Two-factor authentication</h3>
      {error && <div className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {done && <div className="mt-2 rounded bg-green-50 p-2 text-sm text-green-700" role="status">Two-factor login is on.</div>}

      {!me?.totp_enabled ? (
        !setup ? (
          <div className="mt-2 flex items-center gap-3">
            <p className="text-sm text-gray-600 dark:text-gray-300">Require a code from your authenticator app at login.</p>
            <button type="button" onClick={startSetup} disabled={busy} className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
              {busy ? 'Setting up…' : 'Set up'}
            </button>
          </div>
        ) : (
          <form onSubmit={turnOn} className="mt-2 space-y-3">
            <p className="text-sm text-gray-600 dark:text-gray-300">
              Scan this code with your authenticator app, or enter the secret by hand:
            </p>
            <div className="flex flex-wrap items-center gap-4">
              <div className="bg-white p-2 inline-block" dangerouslySetInnerHTML={{ __html: setup.qr_svg }} />
              <div className="space-y-1">
                <code className="block break-all text-sm">{setup.secret}</code>
                <button type="button" onClick={copySecret} className="text-sm text-blue-600 hover:underline">Copy secret</button>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <label htmlFor="totp-enable-code" className="text-sm">Code from your app</label>
              <input
                id="totp-enable-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                className={`${inputClass} w-32`}
                placeholder="123456"
              />
              <button type="submit" disabled={busy || code.length !== 6} className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
                {busy ? 'Turning on…' : 'Turn on'}
              </button>
            </div>
          </form>
        )
      ) : (
        !showOff ? (
          <div className="mt-2 flex items-center gap-3">
            <p className="text-sm text-gray-600 dark:text-gray-300">Two-factor login is on.</p>
            <button type="button" onClick={() => { setShowOff(true); setDone(false) }} className="rounded border border-red-300 px-3 py-1 text-sm text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20">
              Turn off
            </button>
          </div>
        ) : (
          <form onSubmit={turnOff} className="mt-2 flex items-center gap-2">
            <label htmlFor="totp-off-password" className="text-sm">Current password</label>
            <input
              id="totp-off-password"
              type="password"
              value={offPassword}
              onChange={(e) => setOffPassword(e.target.value)}
              className={`${inputClass} w-56`}
            />
            <button type="submit" disabled={busy || !offPassword} className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50">
              {busy ? 'Turning off…' : 'Turn off'}
            </button>
          </form>
        )
      )}
    </div>
  )
}

// Active sessions: live list from /api/v1/auth/sessions (task 086).
function timeAgo(iso: string, now: number): string {
  const diff = Math.max(0, (now - new Date(iso).getTime()) / 1000)
  if (diff < 60) return `${Math.floor(diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)} min ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)} h ago`
  return `${Math.floor(diff / 86400)} d ago`
}

const SessionsCard: React.FC = () => {
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null)
  const [now, setNow] = useState(() => Date.now())

  const load = useCallback(() => {
    getSessionList().then(setSessions).catch(() => setSessions([]))
  }, [])
  useEffect(load, [load])
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(t)
  }, [])

  const signOut = async (s: SessionInfo) => {
    if (!window.confirm(`Sign out ${s.user}'s session from ${s.addr}?`)) return
    try {
      await deleteSession(s.id)
      load()
    } catch { /* ignore */ }
  }

  const ua = (s: SessionInfo) => {
    const parts = s.user_agent.split(' ')
    return parts.length > 1 ? parts[parts.length - 1] : s.user_agent || 'unknown'
  }

  if (!sessions) return null

  return (
    <div className="mt-6 rounded border border-gray-200 p-4 dark:border-gray-600">
      <h3 className="mb-3 text-sm font-semibold text-gray-800 dark:text-white">Active sessions</h3>
      {sessions.length === 0 ? (
        <p className="text-sm text-gray-500">No active sessions.</p>
      ) : (
        <table className="min-w-full text-sm">
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr><th className="px-3 py-2">User</th><th className="px-3 py-2">Address</th><th className="px-3 py-2">Browser</th><th className="px-3 py-2">Signed in</th><th className="px-3 py-2">Last active</th><th className="px-3 py-2"></th></tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.id} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-2 font-medium">{s.user}{s.current && <span className="ml-1"><Badge variant="primary">This session</Badge></span>}</td>
                <td className="px-3 py-2 text-gray-500">{s.addr}</td>
                <td className="px-3 py-2 text-gray-500" title={s.user_agent}>{ua(s)}</td>
                <td className="whitespace-nowrap px-3 py-2 text-gray-500">{new Date(s.created).toLocaleString()}</td>
                <td className="whitespace-nowrap px-3 py-2 text-gray-500">{timeAgo(s.last_seen, now)}</td>
                <td className="whitespace-nowrap px-3 py-2 text-right">
                  {!s.current && (
                    <button type="button" onClick={() => signOut(s)} className="flex items-center gap-1 text-sm text-red-600 hover:underline" aria-label={`Sign out ${s.user}`}>
                      <LogOut size={14} /> Sign out
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
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

// Token scopes, exactly as internal/auth(tokens.go) names them (task 102).
const TOKEN_SCOPES = ['read', 'write:firewall', 'write:network', 'write:system', 'actions', 'commit'] as const

const SCOPE_HINTS: Record<string, string> = {
  read: 'read everything, including the event stream',
  'write:firewall': 'firewall, objects, profiles, feeds',
  'write:network': 'interfaces, DHCP, DNS, routes, VPN',
  'write:system': 'system settings, notifications, snapshots',
  actions: 'blocks, devices, diagnostics, refresh',
  commit: 'commit, confirm, discard, rollback',
}

const SCOPE_PRESETS: { label: string; scopes: readonly string[] }[] = [
  { label: 'Read only', scopes: ['read'] },
  { label: 'Home automation', scopes: ['read', 'write:firewall', 'actions', 'commit'] },
  { label: 'Full', scopes: [] },
]

// tokenExpiry turns a date from an <input type="date"> into the RFC 3339 time
// the API wants: the end of that day, so the token lives all through it.
function tokenExpiry(date: string): string | undefined {
  if (!date) return undefined
  const end = new Date(`${date}T23:59:59`)
  return isNaN(end.getTime()) ? undefined : end.toISOString()
}

// parseCIDRs splits the networks box into valid prefixes and the rest, so the
// form can complain about a typo instead of sending a broken body.
function parseCIDRs(text: string): { cidrs: string[]; bad: string[] } {
  const cidrs: string[] = []
  const bad: string[] = []
  for (const part of text.split(/[,\s]+/)) {
    if (!part) continue
    const m = /^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,2})$/.exec(part)
    const v6 = /^([0-9a-fA-F:]+)\/(\d{1,3})$/.exec(part)
    const ok =
      (m !== null && Number(m[2]) <= 32 && m[1].split('.').every((o) => Number(o) <= 255)) ||
      (v6 !== null && v6[1].includes(':') && Number(v6[2]) <= 128)
    if (ok) cidrs.push(part)
    else bad.push(part)
  }
  return { cidrs, bad }
}

const TokenScopes: React.FC<{ scopes?: string[] }> = ({ scopes }) => {
  if (!scopes || scopes.length === 0) return <Badge>full</Badge>
  return (
    <span className="flex flex-wrap gap-1">
      {scopes.map((s) => (
        <Badge key={s} variant="primary">
          {s}
        </Badge>
      ))}
    </span>
  )
}

const TokenExpiry: React.FC<{ expires?: string; now: Date }> = ({ expires, now }) => {
  if (!expires) return <span aria-label="expiry">never</span>
  const past = new Date(expires).getTime() < now.getTime()
  return (
    <span className={past ? 'font-medium text-red-600' : undefined} aria-label="expiry">
      {past ? 'expired ' : ''}
      {new Date(expires).toLocaleString()}
    </span>
  )
}

const TokensTab: React.FC = () => {
  const [tokens, setTokens] = useState<TokenInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [readonly, setReadonly] = useState(false)
  const [scopes, setScopes] = useState<string[]>([])
  const [expiry, setExpiry] = useState('')
  const [networks, setNetworks] = useState('')
  const [created, setCreated] = useState<string | null>(null)
  // Wall clock for the "expired" colour, refreshed when the list reloads.
  const [now, setNow] = useState(() => new Date())

  const load = useCallback(() => {
    setNow(new Date())
    getTokens().then(setTokens).catch((e: Error) => setError(e.message))
  }, [])
  useEffect(load, [load])

  const toggleScope = (scope: string, on: boolean) =>
    setScopes((cur) => (on ? [...new Set([...cur, scope])] : cur.filter((s) => s !== scope)))

  const create = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!name.trim()) return
    const { cidrs, bad } = parseCIDRs(networks)
    if (bad.length > 0) {
      setError(`Not a CIDR network: ${bad.join(', ')} — want for example 192.168.1.0/24`)
      return
    }
    setError(null)
    try {
      const res = await createToken(
        omitEmpty({
          name: name.trim(),
          readonly,
          scopes: scopes.length > 0 ? [...scopes].sort((a, b) => TOKEN_SCOPES.indexOf(a as (typeof TOKEN_SCOPES)[number]) - TOKEN_SCOPES.indexOf(b as (typeof TOKEN_SCOPES)[number])) : [],
          expires: tokenExpiry(expiry) ?? '',
          allowed_from: cidrs,
        })
      )
      setCreated(res.token)
      setName(''); setReadonly(false); setScopes([]); setExpiry(''); setNetworks('')
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
          <code className="mt-1 blockmin-h-[40px]  break-all rounded bg-white p-2 font-mono text-xs dark:bg-gray-900">{created}</code>
        </div>
      )}
      <form onSubmit={create} className="mb-4 space-y-3">
        <div className="flex flex-wrap items-end gap-2">
          <label className="block text-sm">
            Name
            <input aria-label="Token name" className={inputClass} value={name} onChange={(e) => setName(e.target.value)} placeholder="ci-deploy" />
          </label>
          <label className="flex items-center gap-1 pb-2 text-sm">
            <input type="checkbox" checked={readonly} onChange={(e) => setReadonly(e.target.checked)} /> readonly
          </label>
          <button type="submit" className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">Create</button>
        </div>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-gray-500">Preset</span>
          {SCOPE_PRESETS.map((p) => (
            <button
              key={p.label}
              type="button"
              onClick={() => setScopes([...p.scopes])}
              aria-pressed={
                p.scopes.length === scopes.length && p.scopes.every((s) => scopes.includes(s))
              }
              className="rounded border border-gray-300 px-2 py-1 text-xs hover:bg-gray-100 aria-pressed:border-blue-500 aria-pressed:bg-blue-50 dark:border-gray-600 dark:hover:bg-gray-700"
            >
              {p.label}
            </button>
          ))}
        </div>
        <fieldset className="rounded border border-gray-200 p-2 dark:border-gray-700">
          <legend className="px-1 text-xs text-gray-500">Scopes (check none for a full token)</legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {TOKEN_SCOPES.map((s) => (
              <label key={s} className="flex items-center gap-1 text-sm" title={SCOPE_HINTS[s]}>
                <input
                  type="checkbox"
                  aria-label={`Scope ${s}`}
                  checked={scopes.includes(s)}
                  onChange={(e) => toggleScope(s, e.target.checked)}
                />
                <span className="font-mono text-xs">{s}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <div className="flex flex-wrap items-end gap-2">
          <label className="block text-sm">
            Expires on
            <input type="date" aria-label="Token expiry date" className={inputClass} value={expiry} onChange={(e) => setExpiry(e.target.value)} />
          </label>
          <label className="block flex-1 text-sm">
            Allowed from (CIDR networks, optional)
            <input
              aria-label="Token allowed networks"
              className={inputClass + ' font-mono text-xs'}
              placeholder="192.168.1.0/24, 10.10.0.5/32"
              value={networks}
              onChange={(e) => setNetworks(e.target.value)}
            />
          </label>
        </div>
      </form>
      {tokens.length === 0 ? (
        <p className="text-sm text-gray-500">No tokens yet.</p>
      ) : (
        <table className="min-w-full text-sm">
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr><th className="px-3 py-2">Name</th><th className="px-3 py-2">Scopes</th><th className="px-3 py-2">User</th><th className="px-3 py-2">Role</th><th className="px-3 py-2">Created</th><th className="px-3 py-2">Expires</th><th className="px-3 py-2">Last used</th><th></th></tr>
          </thead>
          <tbody>
            {tokens.map((t) => (
              <tr key={t.id} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-2">
                  {t.name}
                  {t.allowed_from && t.allowed_from.length > 0 && (
                    <span className="block font-mono text-[10px] text-gray-500">{t.allowed_from.join(', ')}</span>
                  )}
                </td>
                <td className="px-3 py-2"><TokenScopes scopes={t.scopes} /></td>
                <td className="px-3 py-2">{t.user}</td>
                <td className="px-3 py-2">{t.role}</td>
                <td className="px-3 py-2 text-xs">{new Date(t.created).toLocaleString()}</td>
                <td className="px-3 py-2 text-xs"><TokenExpiry expires={t.expires} now={now} /></td>
                <td className="px-3 py-2 text-xs" aria-label="last used">{t.last_used ? new Date(t.last_used).toLocaleString() : '—'}</td>
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

      <GitBackupCard />
    </div>
  )
}

// --- Git config history card ----------------------------------------------------

const formatPushTime = (ts: string) => {
  const d = new Date(ts)
  return isNaN(d.getTime())
    ? ts
    : d.toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

const GitBackupCard: React.FC = () => {
  const [status, setStatus] = useState<GitBackupStatus | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [remote, setRemote] = useState('')
  const [branch, setBranch] = useState('main')
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [pushing, setPushing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    getGitBackupStatus()
      .then((s) => {
        setStatus(s)
        setEnabled(s.enabled)
        setRemote(s.remote ?? '')
        setBranch(s.branch || 'main')
      })
      .catch(() => {})
  }, [])

  const copyKey = async () => {
    if (!status?.public_key) return
    try {
      await navigator.clipboard.writeText(status.public_key)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch { /* clipboard unavailable */ }
  }

  const doSave = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setSaving(true); setError(null); setMessage(null)
    try {
      const body: Record<string, unknown> = { enabled }
      if (remote.trim()) body.remote = remote.trim()
      if (branch.trim()) body.branch = branch.trim()
      await patchSection('git_backup', body)
      setMessage('Git backup settings saved. Use the commit bar to apply.')
    } catch (e) {
      setError((e as Error).message)
    } finally { setSaving(false) }
  }

  const doPush = async () => {
    setPushing(true); setError(null); setMessage(null)
    try {
      await pushGitBackup()
      const s = await getGitBackupStatus()
      setStatus(s)
      setMessage('Pushed to remote.')
    } catch (e) {
      setError((e as Error).message)
    } finally { setPushing(false) }
  }

  const doForgetHostKey = async () => {
    setError(null); setMessage(null)
    try {
      await forgetGitBackupHostKey()
      const s = await getGitBackupStatus()
      setStatus(s)
      setMessage('Host key forgotten. Push again to re-pin.')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (!status) return null

  return (
    <section className="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <h2 className="flex items-center gap-2 text-base font-semibold text-gray-800 dark:text-white">
        <GitBranch size={18} /> Config history in git
      </h2>

      {/* Wiki / help section */}
      <div className="rounded border border-blue-100 bg-blue-50 p-3 text-sm dark:border-blue-900/40 dark:bg-blue-900/10">
        <p className="flex items-start gap-2 text-blue-800 dark:text-blue-200">
          <Info size={16} className="mt-0.5 shrink-0" />
          <span>
            Every committed config is saved as a git revision and pushed to your remote over SSH.
            Secrets (password hashes, 2FA secrets, tokens) are left out of the history — only the
            non-secret parts of the config are stored.
          </span>
        </p>
        <div className="mt-2 space-y-1 pl-6 text-xs text-blue-700 dark:text-blue-300">
          <p className="font-medium">Add the deploy key to your repository:</p>
          <ul className="list-disc space-y-0.5 pl-4">
            <li><strong>GitHub:</strong> repository Settings → Deploy keys → Add deploy key, paste the key below and tick "Allow write access"</li>
            <li><strong>Gitea:</strong> repository Settings → Deploy Keys, paste the key</li>
          </ul>
        </div>
      </div>

      {/* Form */}
      <form onSubmit={doSave} className="space-y-3">
        {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
        {message && <div className="rounded bg-green-50 p-2 text-sm text-green-700" role="status">{message}</div>}

        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enable git config history
        </label>

        <label className="block text-sm">
          Remote URL
          <input
            type="text"
            className={inputClass}
            placeholder="git@github.com:user/repo.git"
            value={remote}
            onChange={(e) => setRemote(e.target.value)}
            disabled={!enabled}
          />
        </label>

        <label className="block text-sm">
          Branch
          <input type="text" className={inputClass} value={branch} onChange={(e) => setBranch(e.target.value)} disabled={!enabled} />
        </label>

        {enabled && status.public_key && (
          <div className="space-y-1">
            <span className="block text-xs font-medium text-gray-600 dark:text-gray-400">Deploy key (public)</span>
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-x-auto rounded bg-gray-50 px-3 py-2 font-mono text-xs dark:bg-gray-700/40">{status.public_key}</code>
              <button type="button" onClick={copyKey} aria-label="Copy deploy key" className="rounded border border-gray-300 p-2 hover:bg-gray-50 dark:border-gray-600 dark:hover:bg-gray-700">
                {copied ? <Check size={14} className="text-green-600" /> : <Copy size={14} />}
              </button>
            </div>
          </div>
        )}

        <div className="flex gap-2">
          <button type="submit" disabled={saving} className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
            Save
          </button>
          {enabled && (
            <button type="button" onClick={doPush} disabled={pushing || !remote.trim()} className="flex items-center gap-1 rounded border border-gray-300 px-4 py-1.5 text-sm hover:bg-gray-50 dark:border-gray-600 dark:hover:bg-gray-700 disabled:opacity-50">
              <RefreshCw size={14} className={pushing ? 'animate-spin' : ''} /> {pushing ? 'Pushing…' : 'Test push'}
            </button>
          )}
        </div>
      </form>

      {/* Status */}
      {enabled && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          {status.last_push && (
            <>
              <dt className="font-medium text-gray-600 dark:text-gray-400">Last push</dt>
              <dd>{formatPushTime(status.last_push)}</dd>
            </>
          )}
          {status.pushed_revision && (
            <>
              <dt className="font-medium text-gray-600 dark:text-gray-400">Pushed revision</dt>
              <dd className="font-mono text-xs">{status.pushed_revision}</dd>
            </>
          )}
          {status.pending && (
            <>
              <dt className="font-medium text-gray-600 dark:text-gray-400">Pending</dt>
              <dd>revisions up to {status.last_revision} not pushed yet</dd>
            </>
          )}
        </dl>
      )}

      {status.last_error && (
        <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">
          Last error: {status.last_error}
          {status.host_key && (
            <button type="button" onClick={doForgetHostKey} className="ml-3 text-xs underline hover:no-underline">
              Forget host key and retry
            </button>
          )}
        </div>
      )}
    </section>
  )
}

// --- Import from pfSense --------------------------------------------------------

type ImportStep = 'upload' | 'assign' | 'report'

const ImportTab: React.FC = () => {
  const [step, setStep] = useState<ImportStep>('upload')
  const [file, setFile] = useState<File | null>(null)
  const [devices, setDevices] = useState<Device[]>([])
  const [deviceMap, setDeviceMap] = useState<Record<string, string>>({})
  const [result, setResult] = useState<ImportResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => { getDevices().then(setDevices).catch(() => {}) }, [])

  const doUpload = async (ev: React.ChangeEvent<HTMLInputElement>) => {
    const f = ev.target.files?.[0]
    if (!f) return
    setBusy(true); setError(null); setResult(null)
    try {
      const res = await importPfSense(f)
      setFile(f)
      setResult(res)
      // pre-fill device assignments from pfsense_device names that match a real device
      const map: Record<string, string> = {}
      for (const iface of res.interfaces) {
        if (!iface.vlan && iface.device === '') {
          const match = devices.find((d) => d.name === iface.pfsense_device)
          map[iface.pfsense] = match ? match.name : ''
        }
      }
      setDeviceMap(map)
      // If all non-VLAN interfaces are already assigned, go straight to report
      const unassigned = res.interfaces.filter((i) => !i.vlan && i.device === '')
      if (unassigned.length === 0) {
        setStep('report')
      } else {
        setStep('assign')
      }
    } catch (e) {
      setError((e as Error).message)
    } finally { setBusy(false); ev.target.value = '' }
  }

  const applyAssignment = async () => {
    if (!file) return
    setBusy(true); setError(null)
    try {
      const res = await importPfSense(file, deviceMap)
      setResult(res)
      setStep('report')
    } catch (e) {
      setError((e as Error).message)
    } finally { setBusy(false) }
  }

  if (step === 'upload') {
    return (
      <div className="max-w-2xl space-y-4">
        {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
        <div className="flex items-start gap-2 rounded border border-yellow-200 bg-yellow-50 p-3 text-sm dark:border-yellow-800 dark:bg-yellow-900/20">
          <AlertTriangle size={16} className="mt-0.5 shrink-0 text-yellow-600" />
          <p className="text-yellow-800 dark:text-yellow-200">Importing a pfSense or OPNsense config <strong>replaces the current candidate</strong>. Nothing is committed until you review and commit.</p>
        </div>
        <label className="flex cursor-pointer items-center gap-2 rounded border border-dashed border-gray-300 p-6 text-sm hover:border-blue-400 dark:border-gray-600">
          <FileUp size={20} className="text-gray-400" />
          <span>Upload a pfSense or OPNsense config.xml</span>
          <input type="file" accept=".xml" className="hidden" onChange={doUpload} disabled={busy} aria-label="Upload firewall config" />
        </label>
      </div>
    )
  }

  if (step === 'assign') {
    const rows = result?.interfaces.filter((i) => !i.vlan && i.device === '') ?? []
    return (
      <div className="max-w-3xl space-y-4">
        {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
        <h3 className="text-sm font-semibold text-gray-800 dark:text-white">Assign devices to interfaces</h3>
        <p className="text-xs text-gray-500">Select which physical device backs each {result?.source ?? 'pfSense'} interface. VLANs follow their parent.</p>
        <table className="min-w-full text-sm">
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr><th className="px-3 py-2">{result?.source ?? 'pfSense'}</th><th className="px-3 py-2">Device (from {result?.source ?? 'pfSense'})</th><th className="px-3 py-2">Role</th><th className="px-3 py-2">Assign to</th></tr>
          </thead>
          <tbody>
            {rows.map((iface) => (
              <tr key={iface.pfsense} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-3 py-2 font-medium">{iface.pfsense}</td>
                <td className="px-3 py-2 font-mono text-xs">{iface.pfsense_device || '—'}</td>
                <td className="px-3 py-2"><Badge variant="secondary">{iface.role}</Badge></td>
                <td className="px-3 py-2">
                  <select
                    aria-label={`Device for ${iface.pfsense}`}
                    className="rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700"
                    value={deviceMap[iface.pfsense] ?? ''}
                    onChange={(e) => setDeviceMap((m) => ({ ...m, [iface.pfsense]: e.target.value }))}
                  >
                    <option value="">— unassigned —</option>
                    {devices.map((d) => <option key={d.name} value={d.name}>{d.name}{d.up ? '' : ' (down)'}</option>)}
                  </select>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="flex gap-2">
          <button type="button" onClick={applyAssignment} disabled={busy} className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
            {busy ? 'Applying…' : 'Apply assignment'}
          </button>
          <button type="button" onClick={() => { setStep('upload'); setResult(null); setFile(null) }} className="rounded border border-gray-300 px-4 py-1.5 text-sm dark:border-gray-600">
            Cancel
          </button>
        </div>
      </div>
    )
  }

  // step === 'report'
  return <ImportReport result={result} error={error} onReset={() => { setStep('upload'); setResult(null); setFile(null) }} />
}

const ImportReport: React.FC<{ result: ImportResult | null; error: string | null; onReset: () => void }> = ({ result, error, onReset }) => {
  if (!result) return <p className="text-gray-500">{error ?? 'No report.'}</p>

  const converted = result.report.filter((e) => e.status === 'converted')
  const approximated = result.report.filter((e) => e.status === 'approximated')
  const skipped = result.report.filter((e) => e.status === 'skipped')
  const risks = result.report.filter((e) => e.risk)

  const statusBadge = (s: string) => {
    if (s === 'converted') return <Badge variant="success">converted</Badge>
    if (s === 'approximated') return <Badge variant="warning">approximated</Badge>
    return <Badge variant="secondary">skipped</Badge>
  }

  const Section: React.FC<{ title: string; entries: ImportReportEntry[] }> = ({ title, entries }) => (
    <div className="mb-4">
      <h4 className="mb-1 text-sm font-semibold text-gray-700 dark:text-gray-300">{title} ({entries.length})</h4>
      {entries.length === 0 ? (
        <p className="text-xs text-gray-400">None.</p>
      ) : (
        <ul className="space-y-1">
          {entries.map((e, i) => (
            <li key={i} className={`rounded px-2 py-1.5 text-xs ${e.risk ? 'bg-red-50 dark:bg-red-900/20' : 'bg-gray-50 dark:bg-gray-700/40'}`}>
              <span className="font-mono text-gray-500">{e.section}</span>
              <span className="ml-2 font-medium">{e.item}</span>
              <span className="ml-2">{statusBadge(e.status)}</span>
              {e.risk && <AlertTriangle size={12} className="ml-1 inline text-red-600" />}
              {e.detail && <p className="mt-0.5 text-gray-600 dark:text-gray-400">{e.detail}</p>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )

  return (
    <div className="space-y-4">
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {/* Summary */}
      <div className="flex gap-3 text-sm">
        <span className="rounded bg-green-100 px-2 py-1 text-green-800 dark:bg-green-900/40 dark:text-green-200">{result.summary.converted} converted</span>
        <span className="rounded bg-yellow-100 px-2 py-1 text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200">{result.summary.approximated} approximated</span>
        <span className="rounded bg-gray-100 px-2 py-1 text-gray-700 dark:bg-gray-700 dark:text-gray-300">{result.summary.skipped} skipped</span>
      </div>

      {/* Risk warnings first */}
      {risks.length > 0 && (
        <div className="rounded border border-red-200 bg-red-50 p-3 dark:border-red-800 dark:bg-red-900/20">
          <h4 className="mb-1 flex items-center gap-1 text-sm font-semibold text-red-700 dark:text-red-300"><AlertTriangle size={14} /> Warnings</h4>
          <ul className="space-y-1 text-xs text-red-700 dark:text-red-300">
            {risks.map((e, i) => (
              <li key={i}><span className="font-mono">{e.section}</span>: {e.item} — {e.detail ?? 'review this item'}</li>
            ))}
          </ul>
        </div>
      )}

      {/* Grouped report */}
      <Section title="Converted" entries={converted} />
      <Section title="Approximated" entries={approximated} />
      <Section title="Skipped" entries={skipped} />

      {/* Validation errors */}
      {result.validation_errors && result.validation_errors.length > 0 && (
        <div className="rounded border border-red-200 bg-red-50 p-3 dark:border-red-800 dark:bg-red-900/20">
          <h4 className="mb-1 text-sm font-semibold text-red-700 dark:text-red-300">Validation errors (fix before committing)</h4>
          <ul className="space-y-1 text-xs text-red-700 dark:text-red-300">
            {result.validation_errors.map((ve, i) => (
              <li key={i}><span className="font-mono">{ve.path}</span>: {ve.message}</li>
            ))}
          </ul>
        </div>
      )}

      {/* Actions */}
      <div className="flex gap-2 pt-2">
        <Link to="/system/revisions" className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700">Review changes</Link>
        <button type="button" onClick={onReset} className="rounded border border-gray-300 px-4 py-1.5 text-sm dark:border-gray-600">Import another file</button>
      </div>
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
