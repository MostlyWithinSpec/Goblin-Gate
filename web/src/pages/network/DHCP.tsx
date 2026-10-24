import React, { useEffect, useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  getLeases,
  getNetworkConfig,
  patchSection,
  staticInterfaces,
  type DHCPLease,
  type DHCPServer,
  type NetIf,
} from '../../api/network'
import type { ValidationError } from '../../api/policy'
import { cleanDHCP, validateDHCP, type Errors } from './networkLogic'

// Draft keeps list fields as text while editing.
interface Draft extends DHCPServer {
  dnsText: string
}

const toDraft = (s: DHCPServer): Draft => ({ ...s, static_leases: [...(s.static_leases ?? [])], dnsText: (s.dns ?? []).join(', ') })
const fromDraft = (d: Draft): DHCPServer => {
  const { dnsText, ...s } = d
  const dns = dnsText.split(/[\s,]+/).filter(Boolean)
  return { ...s, dns: dns.length ? dns : undefined }
}

function Leases() {
  const [leases, setLeases] = useState<DHCPLease[] | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    getLeases().then(setLeases).catch((e: Error) => setError(e.message))
  }, [])
  return (
    <section className="mt-6">
      <h3 className="mb-2 font-semibold text-gray-800 dark:text-white">Active leases</h3>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {leases === null && <p className="text-sm text-gray-500">Lease view not available yet.</p>}
      {leases && leases.length === 0 && <p className="text-sm text-gray-500">No active leases.</p>}
      {leases && leases.length > 0 && (
        <div className="max-w-full overflow-x-auto" data-testid="table-scroll">
        <table className="min-w-full text-sm dark:text-gray-200">
          <thead className="text-left text-xs uppercase text-gray-500"><tr><th className="px-3 py-2">IP</th><th className="px-3 py-2">MAC</th><th className="px-3 py-2">Hostname</th><th className="px-3 py-2">Expires</th></tr></thead>
          <tbody>{leases.map((l) => <tr key={l.mac + l.ip} className="border-t border-gray-100 dark:border-gray-700"><td className="px-3 py-1">{l.ip}</td><td className="px-3 py-1 font-mono text-xs">{l.mac}</td><td className="px-3 py-1">{l.hostname}</td><td className="px-3 py-1">{l.expires}</td></tr>)}</tbody>
        </table>
        </div>
      )}
    </section>
  )
}

export default function DHCP() {
  const [servers, setServers] = useState<DHCPServer[]>([])
  const [ifs, setIfs] = useState<NetIf[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [draft, setDraft] = useState<{ d: Draft; index: number | null } | null>(null)
  const [errors, setErrors] = useState<Errors>({})

  useEffect(() => {
    getNetworkConfig()
      .then((cfg) => {
        setServers(cfg.dhcp ?? [])
        setIfs(staticInterfaces(cfg))
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const subnetOf = (name: string) => ifs.find((i) => i.name === name)?.address

  const save = async (next: DHCPServer[]): Promise<boolean> => {
    const prev = servers
    setServers(next)
    setError(null)
    try {
      const res = await patchSection('dhcp', next)
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('dhcp')))
      return true
    } catch (e) {
      setServers(prev)
      setError((e as Error).message)
      return false
    }
  }

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!draft) return
    const s = fromDraft(draft.d)
    const others = servers.filter((_, i) => i !== draft.index)
    const errs = validateDHCP(s, subnetOf(s.interface), others)
    setErrors(errs)
    if (Object.keys(errs).length) return
    const clean = cleanDHCP(s)
    // the form stays open when the save fails, so the edits are not lost
    if (await save(draft.index === null ? [...servers, clean] : servers.map((x, i) => (i === draft.index ? clean : x)))) setDraft(null)
  }

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setDraft((x) => (x ? { ...x, d: { ...x.d, [k]: v } } : x))
  const setLease = (i: number, k: 'mac' | 'ip' | 'hostname', v: string) =>
    setDraft((x) => (x ? { ...x, d: { ...x.d, static_leases: x.d.static_leases!.map((l, j) => (j === i ? { ...l, [k]: v } : l)) } } : x))

  const unserved = ifs.filter((i) => !servers.some((s) => s.interface === i.name))
  const input = 'mt-1 block min-h-[40px] w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'
  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>
  const leaseErrors = Object.entries(errors).filter(([k]) => k.startsWith('static_leases'))

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">DHCP Servers</h2>
        <button
          type="button"
          disabled={!loaded || unserved.length === 0}
          onClick={() => {
            setErrors({})
            setDraft({ d: toDraft({ interface: unserved[0]?.name ?? '', range_start: '', range_end: '' }), index: null })
          }}
          className="flex min-h-[40px] items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
        >
          <Plus size={16} /> New DHCP server
        </button>
      </div>
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      {serverErrors.length > 0 && (
        <ul className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {serverErrors.map((v) => <li key={v.path + v.message}><code>{v.path}</code>: {v.message}</li>)}
        </ul>
      )}

      {/* The table scrolls inside this box, never the page; the interface column
          stays pinned so a row is still identifiable while the rest scrolls. */}
      <div className="max-w-full overflow-x-auto rounded-lg" data-testid="table-scroll">
      <table className="min-w-full rounded bg-white text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
        <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
          <tr><th className="sticky left-0 z-10 bg-gray-50 px-3 py-2 dark:bg-gray-700">Interface</th><th className="px-3 py-2">Range</th><th className="px-3 py-2">Lease</th><th className="px-3 py-2">Static leases</th><th></th></tr>
        </thead>
        <tbody>
          {servers.map((s, i) => (
            <tr key={s.interface} className="border-t border-gray-100 dark:border-gray-700">
              <td className="sticky left-0 z-10 bg-white px-3 py-1 dark:bg-gray-800">
                <button type="button" className="min-h-[40px] text-left text-blue-600 hover:underline" onClick={() => { setErrors({}); setDraft({ d: toDraft(s), index: i }) }}>
                  {s.interface}
                </button>
              </td>
              <td className="px-3 py-1">{s.range_start} – {s.range_end}</td>
              <td className="px-3 py-1">{s.lease_seconds ? `${s.lease_seconds}s` : 'default'}</td>
              <td className="px-3 py-1">{s.static_leases?.length ?? 0}</td>
              <td className="px-3 py-1 text-right">
                <button type="button" aria-label={`Delete DHCP on ${s.interface}`} onClick={() => window.confirm(`Delete the DHCP server on ${s.interface}?`) && save(servers.filter((_, j) => j !== i))} className="inline-flex h-10 w-10 items-center justify-center text-red-600"><Trash2 size={16} /></button>
              </td>
            </tr>
          ))}
          {loaded && servers.length === 0 && <tr><td colSpan={5} className="px-3 py-6 text-center text-gray-500">No DHCP servers.</td></tr>}
        </tbody>
      </table>
      </div>

      {draft && (
        <form onSubmit={submit} noValidate aria-label="DHCP server" className="mt-4 space-y-3 rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300">
          <h3 className="font-semibold">{draft.index === null ? 'New DHCP server' : `DHCP on ${draft.d.interface}`}</h3>
          <label className="block">
            Interface
            <select aria-label="Interface" className={input} value={draft.d.interface} disabled={draft.index !== null} onChange={(e) => set('interface', e.target.value)}>
              {(draft.index === null ? unserved : ifs).map((i) => <option key={i.name} value={i.name}>{i.name} ({i.address})</option>)}
            </select>
            {err('interface')}
          </label>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label>Range start<input aria-label="Range start" className={input} value={draft.d.range_start} onChange={(e) => set('range_start', e.target.value)} />{err('range_start')}</label>
            <label>Range end<input aria-label="Range end" className={input} value={draft.d.range_end} onChange={(e) => set('range_end', e.target.value)} />{err('range_end')}</label>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
            <label>Lease (seconds)<input aria-label="Lease seconds" type="number" min={0} placeholder="default" className={input} value={draft.d.lease_seconds ?? ''} onChange={(e) => set('lease_seconds', e.target.value === '' ? undefined : Number(e.target.value))} />{err('lease_seconds')}</label>
            <label>Gateway<input aria-label="Gateway" placeholder="this box" className={input} value={draft.d.gateway ?? ''} onChange={(e) => set('gateway', e.target.value)} />{err('gateway')}</label>
            <label>DNS servers<input aria-label="DNS servers" placeholder="this box" className={input} value={draft.d.dnsText} onChange={(e) => set('dnsText', e.target.value)} />{err('dns')}</label>
          </div>

          <fieldset>
            <legend className="mb-1 font-medium">Static leases</legend>
            <div className="max-w-full overflow-x-auto">
            <table className="min-w-full">
              <thead className="text-left text-xs uppercase text-gray-500"><tr><th>MAC</th><th>IP</th><th>Hostname</th><th></th></tr></thead>
              <tbody>
                {draft.d.static_leases!.map((l, i) => (
                  <tr key={i}>
                    <td><input aria-label={`Lease ${i + 1} MAC`} className={input} value={l.mac} onChange={(e) => setLease(i, 'mac', e.target.value)} /></td>
                    <td><input aria-label={`Lease ${i + 1} IP`} className={input} value={l.ip} onChange={(e) => setLease(i, 'ip', e.target.value)} /></td>
                    <td><input aria-label={`Lease ${i + 1} hostname`} className={input} value={l.hostname ?? ''} onChange={(e) => setLease(i, 'hostname', e.target.value)} /></td>
                    <td>
                      <button type="button" aria-label={`Remove lease ${i + 1}`} className="inline-flex h-10 w-10 items-center justify-center text-red-600" onClick={() => set('static_leases', draft.d.static_leases!.filter((_, j) => j !== i))}><Trash2 size={14} /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
            {leaseErrors.map(([k, m]) => <p key={k} className="text-xs text-red-600" role="alert">{m}</p>)}
            <button type="button" className="mt-1 min-h-[40px] text-sm text-blue-600" onClick={() => set('static_leases', [...draft.d.static_leases!, { mac: '', ip: '' }])}>+ Add static lease</button>
          </fieldset>
          <div className="flex flex-wrap justify-end gap-2">
            <button type="button" onClick={() => setDraft(null)} className="min-h-[40px] rounded border border-gray-300 px-3 py-1">Cancel</button>
            <button type="submit" className="min-h-[40px] rounded bg-blue-600 px-3 py-1 text-white">Save</button>
          </div>
        </form>
      )}
      <Leases />
    </div>
  )
}
