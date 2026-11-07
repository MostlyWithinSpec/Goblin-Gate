// IPS (task 032): Suricata inline profiles (config "profiles.ips", task 031)
// and recent IPS events with "Create exception" (a suppress entry).
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Checklist, ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { EditDrawer } from '../../components/EditDrawer'
import { getCandidate, getIPSRules, patchSection, type IPSProfile, type IPSRulesStatus } from '../../api/objects'
import { listLogs, type LogEvent } from '../../api/stats'
import { ET_CATEGORIES, addSuppress, blockSeverityText, categoriesLabel, groupBySignature, parseSignature, validateIPS, type Suppress } from './profileLogic'
import { t } from '../../i18n'

// Suricata severity: 1 = high, 2 = medium, 3 = low.
const sevLabel: Record<string, () => string> = { '1': () => t('ips.sevHigh'), '2': () => t('ips.sevMedium'), '3': () => t('ips.sevLow') }
const sevVariant = (s?: string) => (s === '1' ? 'error' : s === '2' ? 'warning' : 'secondary')
const actVariant = (a: string) => (a === 'drop' || a === 'deny' ? 'error' : 'warning')

export function SeverityChip({ severity }: { severity?: string }) {
  const label = sevLabel[severity ?? '']
  return <Badge variant={sevVariant(severity)}>{label ? label() : severity || '?'}</Badge>
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
      onDone(t('ips.exceptionAdded', { sid, profile: target }))
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <EditDrawer isOpen onClose={onClose} title={t('ips.createException')}>
      <div className="space-y-3 text-sm text-gray-700 dark:text-gray-300">
        <p>
          {t('ips.rule')} <code>{sid ?? '?'}</code>: {msg}
        </p>
        {sid === null && <p role="alert" className="text-red-600">{t('ips.noRuleId')}</p>}
        {error && <p role="alert" className="text-red-600">{error}</p>}
        <label className="block">
          {t('ips.profile')}
          <select aria-label="Profile" className={inputClass} value={target} onChange={(e) => setTarget(e.target.value)}>
            {(profiles ?? []).map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
          </select>
        </label>
        <fieldset>
          <legend>{t('ips.silenceRule')}</legend>
          {event.src_ip && (
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === 'by_src'} onChange={() => setScope('by_src')} /> {t('ips.onlyFrom', { ip: event.src_ip })}
            </label>
          )}
          {event.dst_ip && (
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === 'by_dst'} onChange={() => setScope('by_dst')} /> {t('ips.onlyTo', { ip: event.dst_ip })}
            </label>
          )}
          <label className="flex items-center gap-2">
            <input type="radio" name="scope" checked={scope === ''} onChange={() => setScope('')} /> {t('ips.everywhere')}
          </label>
        </fieldset>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border border-gray-300 px-3 py-1">{t('common.cancel')}</button>
          <button type="button" disabled={!target || sid === null} onClick={save} className="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50">{t('ips.addException')}</button>
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
        <h2 className="text-xl font-semibold text-gray-800 dark:text-white">{t('ips.eventsTitle')}</h2>
        <div className="flex items-center gap-3 text-sm">
          <label className="flex items-center gap-1">
            <input type="checkbox" checked={grouped} onChange={(e) => setGrouped(e.target.checked)} /> {t('ips.groupBySignature')}
          </label>
          <Link to={logLink()} className="text-blue-600 hover:underline">{t('ips.openInLog')}</Link>
        </div>
      </div>
      {notice && <p role="status" className="mb-2 rounded bg-green-50 p-2 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-200">{notice}</p>}
      {error && <p role="alert" className="mb-2 rounded bg-red-50 p-2 text-sm text-red-700">{error}</p>}
      <div className="overflow-x-auto rounded bg-white shadow-sm dark:bg-gray-800">
        <table className="min-w-full text-sm text-gray-700 dark:text-gray-200" data-testid="ips-events">
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            {grouped ? (
              <tr><th className="px-3 py-2">{t('ips.colSeverity')}</th><th className="px-3 py-2">{t('ips.colSignature')}</th><th className="px-3 py-2">{t('ips.colCount')}</th><th className="px-3 py-2">{t('ips.colLastSeen')}</th></tr>
            ) : (
              <tr><th className="px-3 py-2">{t('ips.colTime')}</th><th className="px-3 py-2">{t('ips.colSeverity')}</th><th className="px-3 py-2">{t('ips.colSignature')}</th><th className="px-3 py-2">{t('ips.colSrcDst')}</th><th className="px-3 py-2">{t('ips.colAction')}</th><th className="px-3 py-2"></th></tr>
            )}
          </thead>
          <tbody>
            {events === null && !error && <tr><td colSpan={6} className="px-3 py-6 text-center text-gray-500">{t('common.loading')}</td></tr>}
            {events?.length === 0 && <tr><td colSpan={6} className="px-3 py-6 text-center text-gray-500">{t('ips.noEvents')}</td></tr>}
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
                      <button type="button" onClick={() => setException(e)} className="text-blue-600 hover:underline">{t('ips.createException')}</button>
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
      <legend>{t('ips.exceptionsLegend')}</legend>
      {value.length === 0 && <p className="text-xs text-gray-500">{t('ips.noExceptions')}</p>}
      {value.map((s, i) => (
        <div key={i} className="mt-1 flex gap-1">
          <input aria-label={`SID ${i + 1}`} type="number" className={inputClass + ' mt-0 w-28'} value={s.sid || ''} onChange={(e) => set(i, { sid: Number(e.target.value) })} />
          <select aria-label={`Scope ${i + 1}`} className={inputClass + ' mt-0'} value={s.track ?? ''} onChange={(e) => set(i, { track: e.target.value || undefined, ip: e.target.value ? s.ip : undefined })}>
            <option value="">{t('ips.everywhere')}</option>
            <option value="by_src">{t('ips.scopeFrom')}</option>
            <option value="by_dst">{t('ips.scopeTo')}</option>
          </select>
          {s.track && <input aria-label={`IP ${i + 1}`} className={inputClass + ' mt-0'} placeholder="192.168.1.5" value={s.ip ?? ''} onChange={(e) => set(i, { ip: e.target.value.trim() })} />}
          <button type="button" aria-label={`Remove exception ${i + 1}`} onClick={() => onChange(value.filter((_, j) => j !== i))} className="px-1 text-red-600">×</button>
        </div>
      ))}
      <button type="button" onClick={() => onChange([...value, { sid: 0 }])} className="mt-1 text-xs text-blue-600">{t('ips.addExceptionLink')}</button>
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
      <h2 className="text-base font-semibold text-gray-800 dark:text-white">{t('ips.rulesTitle')}</h2>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-300" data-testid="ips-ruleset">
        {status ? (
          <>
            {t('ips.rulesInUse')} <strong>{status.ruleset}</strong>
            {status.rules ? `, ${t('ips.rulesCount', { rules: status.rules.toLocaleString() })}` : ''}
            {status.last_update ? `, ${t('ips.rulesDownloaded', { when: new Date(status.last_update).toLocaleString() })}` : ''}
          </>
        ) : (
          <>{t('ips.rulesInUse')} {stored ? 'ET Pro' : 'ET Open'}</>
        )}
      </p>
      {status?.last_error && (
        <p role="alert" className="mt-1 text-sm text-red-600 dark:text-red-400">
          {t('ips.downloadFailed', { error: status.last_error })}
        </p>
      )}
      <form
        className="mt-3 flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (code.trim()) save({ etpro_code: code.trim() }, t('ips.savedEtPro'))
        }}
      >
        <label className="block text-sm">
          {t('ips.etProCode')}
          <input
            type="password"
            autoComplete="off"
            aria-label="ET Pro code"
            className={inputClass}
            placeholder={stored ? t('ips.unchanged') : t('ips.optional')}
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </label>
        <button type="submit" disabled={!code.trim()} className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
          {t('ips.saveCode')}
        </button>
        {stored && (
          <button type="button" onClick={() => save({}, t('ips.savedEtOpen'))} className="rounded border border-gray-300 px-3 py-1.5 text-sm dark:border-gray-600">
            {t('ips.removeCode')}
          </button>
        )}
      </form>
      <p className="mt-1 text-xs text-gray-500">
        {t('ips.etProHint')}
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
        title={t('ips.title')}
        path="profiles.ips"
        newLabel={t('ips.newProfile')}
        intro={
          <>
            {t('ips.introStart')} <em>{t('ips.modePreventShort')}</em> {t('ips.introPrevent')}
            {t('ips.introPreventRest')} <em>{t('ips.modeDetectShort')}</em> {t('ips.introEnd')}
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
          { label: t('ips.colMode'), render: (p) => (p.mode === 'detect' ? 'detect' : 'prevent') },
          { label: t('ips.colBlocks'), render: (p) => (p.mode === 'detect' ? '—' : blockSeverityText(p.block_severity ?? 2)) },
          { label: t('ips.colCategories'), render: (p) => ((p.categories ?? []).length ? (p.categories ?? []).join(', ') : t('ips.categoriesDefault')) },
          { label: t('ips.colExceptions'), render: (p) => (p.suppress ?? []).length || '' },
        ]}
        after={() => <IPSEvents />}
        form={(p, set, { err }) => (
          <>
            <label className="block">
              {t('ips.comment')}
              <input aria-label="Comment" className={inputClass} value={p.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            </label>
            <label className="block">
              {t('ips.mode')}
              <select aria-label="Mode" className={inputClass} value={p.mode ?? 'prevent'} onChange={(e) => set({ mode: e.target.value as IPSProfile['mode'] })}>
                <option value="prevent">{t('ips.modePrevent')}</option>
                <option value="detect">{t('ips.modeDetect')}</option>
              </select>
            </label>
            {p.mode !== 'detect' && (
              <label className="block">
                {t('ips.blockFromSeverity')}
                <select aria-label="Block severity" className={inputClass} value={p.block_severity ?? 2} onChange={(e) => set({ block_severity: Number(e.target.value) })}>
                  <option value={1}>{t('ips.sevHighOnly')}</option>
                  <option value={2}>{t('ips.sevHighMedium')}</option>
                  <option value={3}>{t('ips.sevAll')}</option>
                </select>
                {err('block_severity')}
              </label>
            )}
            <Checklist
              label={categoriesLabel()}
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

