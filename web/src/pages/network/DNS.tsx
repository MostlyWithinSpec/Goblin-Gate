import React, { useEffect, useState } from 'react'
import { Trash2 } from 'lucide-react'
import { getNetworkConfig, patchSection, type DNSSettings } from '../../api/network'
import type { ValidationError } from '../../api/policy'
import { validateDNS, type Errors } from './networkLogic'

type Rec = { name: string; ip: string }

export default function DNS() {
  const [forwarders, setForwarders] = useState('')
  const [dnssec, setDnssec] = useState(false)
  const [records, setRecords] = useState<Rec[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [errors, setErrors] = useState<Errors>({})
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    getNetworkConfig()
      .then((cfg) => {
        const d = cfg.dns ?? {}
        setForwarders((d.forwarders ?? []).join(', '))
        setDnssec(!!d.dnssec)
        setRecords(Object.entries(d.local_records ?? {}).map(([name, ip]) => ({ name, ip })).sort((a, b) => a.name.localeCompare(b.name)))
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setSaved(false)
    const rows = records.filter((r) => r.name.trim() || r.ip.trim())
    const errs: Errors = {}
    const seen = new Set<string>()
    for (const r of rows) {
      if (seen.has(r.name.trim())) errs[`record.${r.name}`] = `${r.name}: duplicate name`
      seen.add(r.name.trim())
    }
    const dns: DNSSettings = {}
    const fw = forwarders.split(/[\s,]+/).filter(Boolean)
    if (fw.length) dns.forwarders = fw
    if (dnssec) dns.dnssec = true
    if (rows.length) dns.local_records = Object.fromEntries(rows.map((r) => [r.name.trim(), r.ip.trim()]))
    Object.assign(errs, validateDNS(dns))
    setErrors(errs)
    if (Object.keys(errs).length) return
    try {
      const res = await patchSection('dns', dns)
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('dns')))
      setSaved(true)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const setRec = (i: number, k: keyof Rec, v: string) => setRecords((rs) => rs.map((r, j) => (j === i ? { ...r, [k]: v } : r)))
  const input = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'
  const recordErrors = Object.entries(errors).filter(([k]) => k.startsWith('record.'))

  return (
    <form onSubmit={submit} noValidate aria-label="DNS settings" className="space-y-4 text-sm text-gray-700 dark:text-gray-300">
      <h2 className="text-xl font-semibold text-gray-800 dark:text-white">DNS Resolver</h2>
      {error && <div role="alert" className="rounded bg-red-50 p-2 text-red-700">{error}</div>}
      {serverErrors.length > 0 && (
        <ul className="rounded bg-yellow-50 p-2 text-yellow-800">{serverErrors.map((v) => <li key={v.path + v.message}><code>{v.path}</code>: {v.message}</li>)}</ul>
      )}
      <label className="block">
        Forwarders
        <input aria-label="Forwarders" placeholder="empty = resolve from the root servers" className={input} value={forwarders} onChange={(e) => setForwarders(e.target.value)} />
        {errors.forwarders && <p className="mt-1 text-xs text-red-600" role="alert">{errors.forwarders}</p>}
      </label>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={dnssec} onChange={(e) => setDnssec(e.target.checked)} />
        Validate DNSSEC
      </label>
      <fieldset>
        <legend className="mb-1 font-medium">Local records</legend>
        <table className="min-w-full">
          <thead className="text-left text-xs uppercase text-gray-500"><tr><th>Host name</th><th>IPv4 address</th><th></th></tr></thead>
          <tbody>
            {records.map((r, i) => (
              <tr key={i}>
                <td><input aria-label={`Record ${i + 1} name`} className={input} value={r.name} onChange={(e) => setRec(i, 'name', e.target.value)} /></td>
                <td><input aria-label={`Record ${i + 1} address`} className={input} value={r.ip} onChange={(e) => setRec(i, 'ip', e.target.value)} /></td>
                <td><button type="button" aria-label={`Remove record ${i + 1}`} className="text-red-600" onClick={() => setRecords((rs) => rs.filter((_, j) => j !== i))}><Trash2 size={14} /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {recordErrors.map(([k, m]) => <p key={k} className="text-xs text-red-600" role="alert">{m}</p>)}
        <button type="button" className="mt-1 text-blue-600" onClick={() => setRecords((rs) => [...rs, { name: '', ip: '' }])}>+ Add record</button>
      </fieldset>
      <div className="flex items-center gap-3">
        <button type="submit" disabled={!loaded} className="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50">Save</button>
        {saved && <span className="text-green-700">Saved to the candidate config.</span>}
      </div>
    </form>
  )
}
