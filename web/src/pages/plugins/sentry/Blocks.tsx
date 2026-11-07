import React, { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { Dialog } from '../../../components/Dialog'
import { t } from '../../../i18n'
import SentryNav from './SentryNav'
import { getBlocks, createBlock, deleteBlock } from '../../../api/sentry'
import { APIRequestError } from '../../../api/client'

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'

function useNow(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}

// timeLeft spells out a countdown. A const whose value is a t() call at module load
// would freeze English, so the formats are resolved where they render.
function timeLeft(expiresIn: number): string {
  if (expiresIn <= 0) return t('blocks.expired')
  const m = Math.floor(expiresIn / 60)
  const s = expiresIn % 60
  if (m >= 60) return t('blocks.hoursMinutes', { hours: Math.floor(m / 60), minutes: m % 60 })
  return t('blocks.minutesSeconds', { minutes: m, seconds: s })
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
  // refused because the IP is on one of the box's networks: offer the override
  const [isProtected, setIsProtected] = useState(false)

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
      setIsProtected(false)
    },
    onError: (e) => {
      const prot = e instanceof APIRequestError && e.code === 'protected_target'
      setIsProtected(prot)
      setFormError(prot ? `${(e as Error).message}${t('blocks.protectedSuffix')}` : (e as Error).message)
    }
  })

  const submit = () => {
    if (!ip || !reason) {
      setFormError(t('blocks.needIpReason'))
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
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('blocks.title')}</h2>
        <button
          type="button"
          onClick={() => { setFormError(null); setIsProtected(false); setAllowInternal(false); setAdding(true) }}
          className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700"
        >
          <Plus size={14} /> {t('blocks.blockIP')}
        </button>
      </div>

      {q.isLoading && <p className="mt-4 text-sm text-gray-500">{t('common.loading')}</p>}
      {q.isError && (
        <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
          {(q.error as Error).message}
        </div>
      )}

      {blocks.length === 0 && !q.isLoading && (
        <p className="mt-4 text-sm text-gray-500">{t('blocks.noneActive')}</p>
      )}

      <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-800">
            <tr>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('blocks.colIP')}</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('blocks.colReason')}</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('blocks.colSource')}</th>
              <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('blocks.colCreatedBy')}</th>
              <th className="px-3 py-2 text-right text-xs font-medium uppercase text-gray-500">{t('blocks.colTimeLeft')}</th>
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
                    aria-label={t('blocks.unblockAria', { ip: b.ip })}
                    onClick={() => delMut.mutate(b.ip)}
                    disabled={delMut.isPending}
                    className="rounded p-1 text-red-500 hover:bg-red-50 disabled:opacity-50 dark:hover:bg-red-900/30"
                    title={t('blocks.unblock')}
                  >
                    <Trash2 size={14} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Dialog open={adding} onClose={() => setAdding(false)} title={t('blocks.blockIP')} size="small">
        <div className="space-y-4">
          {formError && (
            <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-300">
              {formError}
            </div>
          )}
          <label className="block text-sm">
            {t('blocks.fieldIP')}
            <input type="text" value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.5" className={inputClass} />
          </label>
          <label className="block text-sm">
            {t('blocks.fieldDuration')}
            <input type="number" min={1} max={10080} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} className={inputClass} />
          </label>
          <label className="block text-sm">
            {t('blocks.fieldReason')}
            <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={2} maxLength={500} className={inputClass} />
          </label>
          {isProtected && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={allowInternal} onChange={(e) => setAllowInternal(e.target.checked)} />
              {t('blocks.allowProtected')}
            </label>
          )}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setAdding(false)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">{t('common.cancel')}</button>
            <button
              type="button"
              disabled={addMut.isPending}
              onClick={submit}
              className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700 disabled:opacity-50"
            >
              {addMut.isPending ? t('blocks.blocking') : t('sentry.block')}
            </button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
