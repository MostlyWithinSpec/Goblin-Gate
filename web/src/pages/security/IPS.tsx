// IPS (task 032): Suricata inline profiles (config "profiles.ips", task 031)
// and recent IPS events with "Create exception" (a suppress entry).
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Checklist, ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { EditDrawer } from '../../components/EditDrawer'
import { getCandidate, getIPSRules, patchSection, type IPSProfile, type IPSRulesStatus } from '../../api/objects'
import { listLogs, type LogEvent } from '../../api/stats'
import { ET_CATEGORIES, ET_DEFAULT_EXCLUDED, addSuppress, groupBySignature, parseSignature, validateIPS, type Suppress } from './profileLogic'

// Suricata severity: 1 = high, 2 = medium, 3 = low.
const sevLabel: Record<string, string> = { '1': 'high', '2': 'medium', '3': 'low' }
const sevVariant = (s?: string) => (s === '1' ? 'error' : s === '2' ? 'warning' : 'secondary')
const actVariant = (a: string) => (a === 'drop' || a === 'deny' ? 'error' : 'warning')

export function SeverityChip({ severity }: { severity?: string }) {
  return <Badge variant={sevVariant(severity)}>{sevLabel[severity ?? ''] ?? (severity || '?')}</Badge>
}

// ExceptionDialog adds a suppress entry for an event's rule to a profile.
function ExceptionDialog({ event, onClose, onDone }: { event: LogEvent; onClose: () => void; onDone: (msg: string) => void }) {
  const { sid, msg } = parseSignature(event.signature)
  const [profiles, setProfiles] = useState<IPSProfile[] | null>(null)
  const [target, setTarget] = useState('')
  const [scope, setScope] = useState<'' | 'by_src' | 'by_dst'>(event.src_ip ? 'by_src' : event.dst_ip ? 'by_dst' : '')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    getCandidate()
      .then((c) => {
        const ps = c.profiles?.ips ?? []
        setProfiles(ps)
        setTarget(ps[0]?.name ?? '')
      })
      .catch((e: Error) => setError(e.message))
  }, [])
  const ip = scope === 'by_src' ? event.src_ip : scope === 'by_dst' ? event.dst_ip : undefined
  const save = async () => {
    if (!profiles || sid === null) return
    const s: Suppress = scope ? { sid, track: scope, ip } : { sid }
    const next = profiles.map((p) => (p.name === target ? addSuppress(p, s) : p))
    try {
      await patchSection('profiles.ips', next)
      onDone(`Exception for rule ${sid} added to ${target}. Commit to apply it.`)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <EditDrawer isOpen onClose={onClose} title="Create exception">
      <div className="space-y-3 text-sm text-gray-700 dark:text-gray-300">
        <p>
          Rule <code>{sid ?? '?'}</code>: {msg}
        </p>
        {sid === null && <p role="alert" className="text-red-600">This event has no rule id.</p>}
        {error && <p role="alert" className="text-red-600">{error}</p>}
        <label className="block">
          Profile
          <select aria-label="Profile" className={inputClass} value={target} onChange={(e) => setTarget(e.target.value)}>
            {(profiles ?? []).map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
          </select>
        </label>
        <fieldset>
          <legend>Silence the rule</legend>
          {event.src_ip && (
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === 'by_src'} onChange={() => setScope('by_src')} /> only from {event.src_ip}
            </label>
          )}
          {event.dst_ip && (
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === 'by_dst'} onChange={() => setScope('by_dst')} /> only to {event.dst_ip}
            </label>
          )}
          <label className="flex items-center gap-2">
            <input type="radio" name="scope" checked={scope === ''} onChange={() => setScope('')} /> everywhere
          </label>
        </fieldset>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border border-gray-300 px-3 py-1">Cancel</button>
          <button type="button" disabled={!target || sid === null} onClick={save} className="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50">Add exception</button>
        </div>
      </div>
    </EditDrawer>
  )
}

export function IPSEvents() {
  const [events, setEvents] = useState<LogEvent[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [grouped, setGrouped] = useState(false)
  const [exception, setException] = useState<LogEvent | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  useEffect(() => {
    listLogs({ kind: 'ips', from: '-24h', limit: 500 })
      .then((p) => setEvents(p.events ?? []))
      .catch((e: Error) => setError(e.message))
  }, [])
  const logLink = (sig?: string) => `/log?${new URLSearchParams({ kind: 'ips', from: '-24h', ...(sig ? { signature: sig } : {}) })}`

  return (
    <div className="mt-8">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">Events (last 24 h)</h2>
        <div className="flex items-center gap-3 text-sm">
          <label className="flex items-center gap-1">
            <input type="checkbox" checked={grouped} onChange={(e) => setGrouped(e.target.checked)} /> Group by signature
          </label>
          <Link to={logLink()} className="text-blue-600 hover:underline">Open in log viewer</Link>
        </div>
      </div>
      {notice && <p role="status" className="mb-2 rounded bg-green-50 p-2 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-200">{notice}</p>}
      {error && <p role="alert" className="mb-2 rounded bg-red-50 p-2 text-sm text-red-700">{error}</p>}
      <div className="overflow-x-auto rounded bg-white shadow-sm dark:bg-gray-800">
        <table className="min-w-full text-sm text-gray-700 dark:text-gray-200" data-testid="ips-events">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            {grouped ? (
              <tr><th className="px-3 py-2">Severity</th><th className="px-3 py-2">Signature</th><th className="px-3 py-2">Count</th><th className="px-3 py-2">Last seen</th></tr>
            ) : (
              <tr><th className="px-3 py-2">Time</th><th className="px-3 py-2">Severity</th><th className="px-3 py-2">Signature</th><th className="px-3 py-2">Source → Destination</th><th className="px-3 py-2">Action</th><th className="px-3 py-2"></th></tr>
            )}
          </thead>
          <tbody>
            {events === null && !error && <tr><td colSpan={6} className="px-3 py-6 text-center text-gray-500">Loading…</td></tr>}
            {events?.length === 0 && <tr><td colSpan={6} className="px-3 py-6 text-center text-gray-500">No IPS events in the last 24 hours.</td></tr>}
            {grouped
              ? groupBySignature(events ?? []).map((g) => (
                  <tr key={g.signature} className="border-t border-gray-100 dark:border-gray-700">
                    <td className="px-3 py-1"><SeverityChip severity={g.severity} /></td>
                    <td className="px-3 py-1"><Link to={logLink(g.signature)} className="text-blue-600 hover:underline">{parseSignature(g.signature).msg}</Link></td>
                    <td className="px-3 py-1">{g.count}</td>
                    <td className="px-3 py-1">{new Date(g.last * 1000).toLocaleString()}</td>
                  </tr>
                ))
              : (events ?? []).map((e) => (
                  <tr key={e.id} className="border-t border-gray-100 dark:border-gray-700">
                    <td className="whitespace-nowrap px-3 py-1">{new Date(e.time * 1000).toLocaleString()}</td>
                    <td className="px-3 py-1"><SeverityChip severity={e.severity} /></td>
                    <td className="px-3 py-1">{parseSignature(e.signature).msg}</td>
                    <td className="whitespace-nowrap px-3 py-1">{e.src_ip}{e.src_port ? `:${e.src_port}` : ''} → {e.dst_ip}{e.dst_port ? `:${e.dst_port}` : ''}</td>
                    <td className="px-3 py-1"><Badge variant={actVariant(e.action)}>{e.action}</Badge></td>
                    <td className="px-3 py-1 text-right">
                      <button type="button" onClick={() => setException(e)} className="text-blue-600 hover:underline">Create exception</button>
                    </td>
                  </tr>
                ))}
          </tbody>
        </table>
      </div>
      {exception && (
        <ExceptionDialog
          event={exception}
          onClose={() => setException(null)}
          onDone={(m) => {
            setNotice(m)
            setException(null)
          }}
        />
      )}
    </div>
  )
}

function SuppressList({ value, onChange }: { value: Suppress[]; onChange: (v: Suppress[]) => void }) {
  const set = (i: number, patch: Partial<Suppress>) => onChange(value.map((s, j) => (j === i ? { ...s, ...patch } : s)))
  return (
    <fieldset>
      <legend>Exceptions (silenced rules)</legend>
      {value.length === 0 && <p className="text-xs text-gray-500">None. Use "Create exception" on an event below to add one.</p>}
      {value.map((s, i) => (
        <div key={i} className="mt-1 flex gap-1">
          <input aria-label={`SID ${i + 1}`} type="number" className={inputClass + ' mt-0 w-28'} value={s.sid || ''} onChange={(e) => set(i, { sid: Number(e.target.value) })} />
          <select aria-label={`Scope ${i + 1}`} className={inputClass + ' mt-0'} value={s.track ?? ''} onChange={(e) => set(i, { track: e.target.value || undefined, ip: e.target.value ? s.ip : undefined })}>
            <option value="">everywhere</option>
            <option value="by_src">from</option>
            <option value="by_dst">to</option>
          </select>
          {s.track && <input aria-label={`IP ${i + 1}`} className={inputClass + ' mt-0'} placeholder="192.168.1.5" value={s.ip ?? ''} onChange={(e) => set(i, { ip: e.target.value.trim() })} />}
          <button type="button" aria-label={`Remove exception ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))} className="px-1 text-red-600">×</button>
        </div>
      ))}
      <button type="button" onClick={() => onChange([...value, { sid: 0 }])} className="mt-1 text-xs text-blue-600">+ Add exception</button>
    </fieldset>
  )
}

// RuleSource shows which rules the IPS uses and takes an ET Pro code: with a
// code the daily download fetches ET Pro instead of ET Open (after commit).
export function RuleSource() {
  const [status, setStatus] = useState<IPSRulesStatus | null>(null)
  const [stored, setStored] = useState(false)
  const [code, setCode] = useState('')
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null)
  useEffect(() => {
    getIPSRules().then(setStatus).catch(() => setStatus(null))
    getCandidate()
      .then((c) => setStored(!!c.ips?.etpro_code))
      .catch(() => {})
  }, [])
  const save = async (value: { etpro_code?: string }, done: string) => {
    try {
      const res = await patchSection('ips', value)
      const err = res.validation_errors?.find((e) => e.path.startsWith('ips'))
      if (err) {
        setMessage({ ok: false, text: err.message })
        return
      }
      setStored(!!value.etpro_code)
      setCode('')
      setMessage({ ok: true, text: done })
    } catch (e) {
      setMessage({ ok: false, text: (e as Error).message })
    }
  }
  return (
    <section aria-label="Rule source" className="mb-6 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <h2 className="text-base font-semibold text-gray-800 dark:text-white">Rules</h2>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-300" data-testid="ips-ruleset">
        {status ? (
          <>
            In use: <strong>{status.ruleset}</strong>
            {status.rules ? `, ${status.rules.toLocaleString()} rules` : ''}
            {status.last_update ? `, downloaded ${new Date(status.last_update).toLocaleString()}` : ''}
          </>
        ) : (
          <>In use: {stored ? 'ET Pro' : 'ET Open'}</>
        )}
      </p>
      {status?.last_error && (
        <p role="alert" className="mt-1 text-sm text-red-600 dark:text-red-400">
          Last download failed: {status.last_error}
        </p>
      )}
      <form
        className="mt-3 flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (code.trim()) save({ etpro_code: code.trim() }, 'Saved. Commit to switch to ET Pro; the rules download right after.')
        }}
      >
        <label className="block text-sm">
          ET Pro code
          <input
            type="password"
            autoComplete="off"
            aria-label="ET Pro code"
            className={inputClass}
            placeholder={stored ? 'unchanged' : 'optional'}
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </label>
        <button type="submit" disabled={!code.trim()} className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
          Save code
        </button>
        {stored && (
          <button type="button" onClick={() => save({}, 'Saved. Commit to go back to ET Open.')} className="rounded border border-gray-300 px-3 py-1.5 text-sm dark:border-gray-600">
            Remove code
          </button>
        )}
      </form>
      <p className="mt-1 text-xs text-gray-500">
        With an ET Pro subscription from Proofpoint, paste its code to use ET Pro instead of the free ET Open rules.
      </p>
      {message && (
        <p role={message.ok ? 'status' : 'alert'} className={`mt-2 text-sm ${message.ok ? 'text-green-700 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
          {message.text}
        </p>
      )}
    </section>
  )
}

export default function IPS() {
  return (
    <div className="p-6">
      <RuleSource />
      <ConfigList<IPSProfile>
        title="Intrusion Prevention (IPS)"
        path="profiles.ips"
        newLabel="New IPS profile"
        intro={
          <>
            Each profile in use runs its own Suricata with the ET Open (or ET Pro) rules, downloaded daily. <em>Prevent</em> drops traffic that matches rules at or above
            the blocking severity and only logs the rest; <em>detect</em> only logs. Pick a profile in a policy to inspect its traffic.
          </>
        }
        select={(c) => c.profiles?.ips}
        empty={() => ({ name: '', mode: 'prevent', block_severity: 2 })}
        clean={(p) =>
          omitEmpty({
            ...p,
            comment: p.comment?.trim(),
            mode: p.mode === 'prevent' ? undefined : p.mode,
            block_severity: p.block_severity === 2 ? undefined : p.block_severity,
            suppress: p.suppress?.map((s) => omitEmpty(s)),
          })
        }
        validate={validateIPS}
        columns={[
          { label: 'Mode', render: (p) => (p.mode === 'detect' ? 'detect' : 'prevent') },
          { label: 'Blocks', render: (p) => (p.mode === 'detect' ? '—' : ({ 1: 'high', 2: 'high + medium', 3: 'all' } as Record<number, string>)[p.block_severity ?? 2]) },
          { label: 'Categories', render: (p) => ((p.categories ?? []).length ? (p.categories ?? []).join(', ') : 'default') },
          { label: 'Exceptions', render: (p) => (p.suppress ?? []).length || '' },
        ]}
        after={() => <IPSEvents />}
        form={(p, set, { err }) => (
          <>
            <label className="block">
              Comment
              <input aria-label="Comment" className={inputClass} value={p.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            </label>
            <label className="block">
              Mode
              <select aria-label="Mode" className={inputClass} value={p.mode ?? 'prevent'} onChange={(e) => set({ mode: e.target.value as IPSProfile['mode'] })}>
                <option value="prevent">Prevent (block)</option>
                <option value="detect">Detect only (log)</option>
              </select>
            </label>
            {p.mode !== 'detect' && (
              <label className="block">
                Block from severity
                <select aria-label="Block severity" className={inputClass} value={p.block_severity ?? 2} onChange={(e) => set({ block_severity: Number(e.target.value) })}>
                  <option value={1}>High only</option>
                  <option value={2}>High and medium</option>
                  <option value={3}>All</option>
                </select>
                {err('block_severity')}
              </label>
            )}
            <Checklist
              label={`Rule categories (none ticked = all but ${ET_DEFAULT_EXCLUDED.join(', ')})`}
              options={[...new Set([...ET_CATEGORIES, ...(p.categories ?? [])])]}
              value={p.categories ?? []}
              onChange={(categories) => set({ categories })}
            />
            {err('categories')}
            <SuppressList value={p.suppress ?? []} onChange={(suppress) => set({ suppress })} />
            {err('suppress')}
          </>
        )}
      />
    </div>
  )
}

