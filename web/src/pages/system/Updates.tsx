import React, { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw, ExternalLink, CheckCircle2, AlertTriangle, RotateCcw, XCircle } from 'lucide-react'
import { getCandidate, patchSection, type SystemSettings } from '../../api/objects'
import { inputClass } from '../../components/ConfigList'
import { getUpdates, applyUpdate, type UpdatesStatus } from '../../api/system'

const CHANNELS = ['stable', 'beta', 'nightly'] as const
type Channel = (typeof CHANNELS)[number]

// Debian versions use "~" which is not friendly in a UI; show "-" instead.
export const displayVersion = (v: string) => v.replace(/~/g, '-')

const Updates: React.FC = () => {
  const [status, setStatus] = useState<UpdatesStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)
  const [channel, setChannel] = useState<Channel>('stable')
  const [channelSaved, setChannelSaved] = useState(false)
  const [includeOs, setIncludeOs] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [applying, setApplying] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const load = useCallback(async (refresh: boolean) => {
    try {
      setError(null)
      if (refresh) setChecking(true)
      const s = await getUpdates(refresh)
      setStatus(s)
      return s
    } catch (e) {
      setError((e as Error).message)
      return null
    } finally {
      setChecking(false)
    }
  }, [])

  useEffect(() => {
    load(false)
    getCandidate()
      .then((c) => {
        const ch = c.system?.update_channel
        if (ch && CHANNELS.includes(ch)) setChannel(ch)
      })
      .catch(() => { /* channel stays at default */ })
  }, [load])

  useEffect(() => () => { if (pollRef.current) clearInterval(pollRef.current) }, [])

  const saveChannel = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setChannelSaved(false)
    try {
      const keep: Partial<SystemSettings> = { ...(await getCandidate()).system }
      await patchSection('system', { ...keep, update_channel: channel })
      setChannelSaved(true)
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const startPolling = () => {
    if (pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const s = await getUpdates(false)
        setStatus(s)
        setRestarting(true)
        if (s.last && s.last.state !== 'running') {
          if (pollRef.current) clearInterval(pollRef.current)
          pollRef.current = null
          setApplying(false)
          setRestarting(false)
        }
      } catch {
        // goblind is restarting — keep polling.
        setRestarting(true)
      }
    }, 5000)
  }

  const doUpdate = async () => {
    setConfirming(false)
    setApplying(true)
    setError(null)
    try {
      await applyUpdate({ version: status?.available || undefined, include_os: includeOs })
      startPolling()
    } catch (e) {
      setApplying(false)
      setError((e as Error).message)
    }
  }

  if (!status) return <p className="text-gray-500">{error ?? 'Loading…'}</p>

  const available = status.available && status.available !== ''
  const last = status.last

  return (
    <div className="max-w-2xl space-y-6">
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}

      {/* Installed / available */}
      <section className="space-y-2">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Version</h2>
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt className="font-medium text-gray-600 dark:text-gray-400">Installed</dt>
          <dd>{displayVersion(status.installed)}</dd>
          <dt className="font-medium text-gray-600 dark:text-gray-400">Available</dt>
          <dd>
            {available ? (
              <span className="inline-flex items-center gap-2">
                <span className="rounded bg-green-100 px-2 py-0.5 font-medium text-green-800">{displayVersion(status.available!)}</span>
                {status.release_url && (
                  <a href={status.release_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-blue-600 hover:underline">
                    Release notes <ExternalLink size={12} />
                  </a>
                )}
              </span>
            ) : status.checked ? (
              <span className="text-gray-500">Up to date</span>
            ) : (
              <span className="text-gray-400">Not checked yet</span>
            )}
          </dd>
          {status.os_updates !== undefined && status.os_updates > 0 && (
            <>
              <dt className="font-medium text-gray-600 dark:text-gray-400">OS updates</dt>
              <dd>{status.os_updates} Debian package update{status.os_updates === 1 ? '' : 's'} pending</dd>
            </>
          )}
        </dl>
        {status.check_error && <p className="text-xs text-yellow-700">Last check failed: {status.check_error}</p>}
        <button
          type="button"
          onClick={() => load(true)}
          disabled={checking || applying}
          className="inline-flex items-center gap-1 rounded border border-gray-300 px-3 py-1.5 text-sm dark:border-gray-600 disabled:opacity-50"
        >
          <RefreshCw size={14} className={checking ? 'animate-spin' : ''} /> {checking ? 'Checking…' : 'Check for updates'}
        </button>
      </section>

      {/* Channel */}
      <form onSubmit={saveChannel} className="space-y-2">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Update channel</h2>
        {channelSaved && <div className="rounded bg-green-50 p-2 text-sm text-green-700" role="status">Channel saved. Use the commit bar to apply.</div>}
        <label className="block text-sm">
          Channel
          <select aria-label="Update channel" className={inputClass} value={channel} onChange={(e) => setChannel(e.target.value as Channel)}>
            {CHANNELS.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <button type="submit" className="rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700">Save channel</button>
      </form>

      {/* Update now */}
      <section className="space-y-2">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Update now</h2>
        {available ? (
          <>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={includeOs} onChange={(e) => setIncludeOs(e.target.checked)} />
              Also install Debian updates{status.os_updates !== undefined && ` (${status.os_updates})`}
            </label>
            {!applying && (
              <button
                type="button"
                onClick={() => setConfirming(true)}
                className="rounded bg-green-600 px-4 py-1.5 text-sm text-white hover:bg-green-700"
              >
                Update now
              </button>
            )}
          </>
        ) : (
          <p className="text-sm text-gray-500">No update available.</p>
        )}

        {applying && (
          <div className="rounded bg-blue-50 p-3 text-sm" role="status">
            <p className="font-medium text-blue-800">{restarting ? 'Goblin Gate is restarting…' : 'Update in progress…'}</p>
            {last?.step && <p className="mt-1 text-blue-700">{last.step}</p>}
          </div>
        )}

        {!applying && last && (
          <ResultBox last={last} />
        )}
      </section>

      {/* Confirm dialog */}
      {confirming && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" role="dialog" aria-modal="true">
          <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl dark:bg-gray-800">
            <h3 className="mb-2 text-base font-semibold text-gray-800 dark:text-white">Update now?</h3>
            <p className="mb-4 text-sm text-gray-600 dark:text-gray-300">
              This box will restart during the update. If the new version fails, Goblin Gate rolls back automatically.
              {includeOs && ' Debian updates will also be installed.'}
            </p>
            <div className="flex justify-end gap-2">
              <button type="button" onClick={() => setConfirming(false)} className="rounded border border-gray-300 px-3 py-1.5 text-sm dark:border-gray-600">Cancel</button>
              <button type="button" onClick={doUpdate} className="rounded bg-green-600 px-3 py-1.5 text-sm text-white hover:bg-green-700">Update now</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const ResultBox: React.FC<{ last: NonNullable<UpdatesStatus['last']> }> = ({ last }) => {
  const map = {
    done: { icon: CheckCircle2, cls: 'bg-green-50 text-green-800', title: 'Update complete' },
    rolled_back: { icon: RotateCcw, cls: 'bg-yellow-50 text-yellow-800', title: 'Update rolled back' },
    failed: { icon: XCircle, cls: 'bg-red-50 text-red-800', title: 'Update failed' },
    running: { icon: AlertTriangle, cls: 'bg-blue-50 text-blue-800', title: 'Update in progress' },
  } as const
  const m = map[last.state]
  const Icon = m.icon
  return (
    <div className={`rounded p-3 text-sm ${m.cls}`} role="status">
      <p className="flex items-center gap-2 font-medium"><Icon size={16} /> {m.title}</p>
      {(last.from || last.to) && (
        <p className="mt-1">{displayVersion(last.from ?? '')} → {displayVersion(last.to ?? '')}</p>
      )}
      {last.message && <p className="mt-1 whitespace-pre-wrap text-xs">{last.message}</p>}
    </div>
  )
}

export default Updates
