import React, { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { Dialog } from '../../../components/Dialog'
import SentryNav from './SentryNav'
import { getBlocks, createBlock, deleteBlock } from '../../../api/sentry'

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'

function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

function timeLeft(expiresIn: number): string {
  if (expiresIn <= 0) return 'expired'
  const m = Math.floor(expiresIn / 60)
  const s = expiresIn % 60
  if (m >= 60) return `${Math.floor(m / 60)}h ${m % 60}m`
  return `${m}m ${s}s`
}

export default function Blocks() {
  const qc = useQueryClient()
  const now = useNow(1000)
  const [adding, setAdding] = useState(false)
  const [ip, setIp] = useState('')
  const [minutes, setMinutes] = useState(60)
  const [reason, setReason] = useState('')
  const [allowInternal, setAllowInternal] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const q = useQuery({ queryKey: ['sentry', 'blocks'], queryFn: getBlocks, retry: false, refetchInterval: 10000 })

  const delMut = useMutation({
    mutationFn: (blockIp: string) => deleteBlock(blockIp),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sentry', 'blocks'] })
  })

  const addMut = useMutation({
    mutationFn: () => createBlock({ ip, minutes, reason, allow_internal: allowInternal }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sentry', 'blocks'] })
      setAdding(false)
      setIp('')
      setReason('')
      setAllowInternal(false)
      setFormError(null)
    },
    onError: (e) => {
      const msg = (e as Error).message
      if (msg.includes('protected_target')) {
        setFormError('This IP is inside one of the box’s own networks. An admin can allow this explicitly.')
      } else {
        setFormError(msg)
      }
    }
  })

  const submit = () => {
    if (!ip || !reason) {
      setFormError('IP and reason are required.')
      return
    }
    addMut.mutate()
  }

  const blocks = (q.data?.blocks ?? []).map((b) => ({
    ...b,
    remaining: Math.max(0, new Date(b.until).getTime() - now) / 1000
  }))

  return (
    <div>
      <SentryNav />

      <div className="mt-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">Active blocks</h2>
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700"
        >
          <Plus size={14} /> Block IP
        </button>
      </div>

      {q.isLoading && <p className="mt-4 text-sm text-gray-500">Loading…</p>}
      {q.isError && (
        <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
          {(q.error as Error).message}
        </div>
      )}

      {blocks.length === 0 && !q.isLoading && (
        <p className="mt-4 text-sm text-gray-500">No active blocks.</p>
      )}

      <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-800">
            <tr>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">IP</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Reason</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Source</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Created by</th>
              <th className="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500">Time left</th>
              <th className="w-16 px-2 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-700/50 dark:bg-gray-800">
            {blocks.map((b) => (
              <tr key={b.ip} className="hover:bg-gray-50 dark:hover:bg-gray-700/40">
                <td className="px-3 py-2 font-mono text-sm">{b.ip}</td>
                <td className="max-w-xs truncate px-3 py-2 text-sm text-gray-500" title={b.reason}>{b.reason}</td>
                <td className="px-3 py-2"><Badge variant={b.source === 'admin' ? 'primary' : 'secondary'}>{b.source}</Badge></td>
                <td className="px-3 py-2 text-sm text-gray-500">{b.created_by}</td>
                <td className={`whitespace-nowrap px-3 py-2 text-right font-mono text-sm ${b.remaining <= 300 ? 'text-red-600' : 'text-gray-500'}`}>
                  {timeLeft(b.remaining)}
                </td>
                <td className="px-2 py-2">
                  <button
                    type="button"
                    onClick={() => delMut.mutate(b.ip)}
                    disabled={delMut.isPending}
                    className="rounded p-1 text-red-500 hover:bg-red-50 disabled:opacity-50 dark:hover:bg-red-900/30"
                    title="Unblock"
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Dialog open={adding} onClose={() => setAdding(false)} title="Block IP" size="small">
        <div className="space-y-4">
          {formError && (
            <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-300">
              {formError}
            </div>
          )}
          <label className="block text-sm">
            IP address
            <input type="text" value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.5" className={inputClass} />
          </label>
          <label className="block text-sm">
            Duration (minutes, 1–10080)
            <input type="number" min={1} max={10080} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} className={inputClass} />
          </label>
          <label className="block text-sm">
            Reason (required, ≤500 chars)
            <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} maxLength={500} className={inputClass} />
          </label>
          {formError?.includes('protected_target') && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={allowInternal} onChange={(e) => setAllowInternal(e.target.checked)} />
              Allow (IP is inside one of the box’s own networks)
            </label>
          )}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setAdding(false)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">Cancel</button>
            <button
              type="button"
              disabled={addMut.isPending}
              onClick={submit}
              className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50"
            >
              {addMut.isPending ? 'Blocking…' : 'Block'}
            </button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
