import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../../components/Button'
import { Badge } from '../../components/Badge'
import { Dialog } from '../../components/Dialog'
import { listSnapshots, saveSnapshot, restoreSnapshot, deleteSnapshot, SnapshotInfo } from '../../api/snapshots'
import { getConfigDiff } from '../../api/config'
import { t } from '../../i18n'

const formatTime = (ts: string) => {
  const d = new Date(ts)
  return isNaN(d.getTime())
    ? ts
    : d.toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

const th = 'px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider'

export const Snapshots: React.FC = () => {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [comment, setComment] = useState('')
  const [restoreTarget, setRestoreTarget] = useState<SnapshotInfo | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SnapshotInfo | null>(null)
  const [diffCount, setDiffCount] = useState<number | null>(null)

  const { data: snapshots, isLoading, error } = useQuery({ queryKey: ['snapshots'], queryFn: listSnapshots })
  const list = snapshots ?? []

  const saveMutation = useMutation({
    mutationFn: () => saveSnapshot(name.trim(), comment.trim()),
    onSuccess: (info) => {
      setName('')
      setComment('')
      queryClient.invalidateQueries({ queryKey: ['snapshots'] })
      void info
    },
  })

  const restoreMutation = useMutation({
    mutationFn: (n: string) => restoreSnapshot(n),
    onSuccess: async () => {
      // Refresh the candidate diff so the commit bar / user can see what changed.
      try {
        const d = await getConfigDiff()
        setDiffCount(d.count)
      } catch {
        setDiffCount(null)
      }
      queryClient.invalidateQueries({ queryKey: ['snapshots'] })
      queryClient.invalidateQueries({ queryKey: ['configDiff'] }) // the commit bar
      setRestoreTarget(null)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (n: string) => deleteSnapshot(n),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['snapshots'] })
      setDeleteTarget(null)
    },
  })

  if (isLoading) {
    return <div className="p-4">{t('snapshots.loading')}</div>
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">{t('snapshots.title')}</h1>
      <p className="text-sm text-gray-600 dark:text-gray-400">
        {t('snapshots.intro')}
      </p>

      {/* Save form */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 space-y-3">
        <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-200">{t('snapshots.saveHeading')}</h2>
        <div className="flex flex-col sm:flex-row gap-3">
          <label className="flex-1">
            <span className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">{t('snapshots.name')}</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('snapshots.namePlaceholder')}
              className="w-full rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 min-h-[40px]"
            />
          </label>
          <label className="flex-1">
            <span className="block text-xs font-medium text-gray-600 dark:text-gray-400 mb-1">{t('snapshots.comment')}</span>
            <input
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              placeholder={t('snapshots.commentPlaceholder')}
              className="w-full rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 min-h-[40px]"
            />
          </label>
        </div>
        {saveMutation.isError && (
          <p className="text-sm text-red-600 dark:text-red-400" role="alert">
            {(saveMutation.error as Error).message}
          </p>
        )}
        <div>
          <Button variant="primary" size="sm" disabled={!name.trim() || saveMutation.isPending} onClick={() => saveMutation.mutate()}>
            {t('snapshots.save')}
          </Button>
        </div>
      </div>

      {error != null && (
        <p className="text-sm text-red-600 dark:text-red-400" role="alert">
          {(error as Error).message}
        </p>
      )}

      {/* Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-700">
            <tr>
              <th className={th}>{t('snapshots.colName')}</th>
              <th className={th}>{t('snapshots.colComment')}</th>
              <th className={th}>{t('snapshots.colCreated')}</th>
              <th className={th}>{t('snapshots.colBy')}</th>
              <th className={th}>{t('snapshots.colRev')}</th>
              <th className={th}>{t('snapshots.colActions')}</th>
            </tr>
          </thead>
          <tbody className="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
            {list.map((s) => (
              <tr key={s.name} className="hover:bg-gray-50 dark:hover:bg-gray-700">
                <td className="px-6 py-4 whitespace-nowrap font-medium text-gray-900 dark:text-white">{s.name}</td>
                <td className="px-6 py-4 text-gray-600 dark:text-gray-300">{s.comment}</td>
                <td className="px-6 py-4 whitespace-nowrap text-gray-600 dark:text-gray-300">{formatTime(s.created)}</td>
                <td className="px-6 py-4 whitespace-nowrap">
                  <Badge variant="secondary">{s.created_by}</Badge>
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-gray-600 dark:text-gray-300">{s.revision || '—'}</td>
                <td className="px-6 py-4 whitespace-nowrap text-sm font-medium space-x-2">
                  <Button variant="secondary" size="sm" onClick={() => setRestoreTarget(s)}>
                    {t('snapshots.restore')}
                  </Button>
                  <Button variant="danger" size="sm" onClick={() => setDeleteTarget(s)}>
                    {t('snapshots.delete')}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {list.length === 0 && <div className="text-center py-8 text-gray-500">{t('snapshots.empty')}</div>}
      </div>

      {/* Restore confirm */}
      <Dialog open={restoreTarget !== null} onClose={() => setRestoreTarget(null)} title={t('snapshots.confirmRestoreTitle')} size="small">
        <div className="py-2">
          <p>{t('snapshots.confirmRestore', { name: restoreTarget?.name ?? '' })}</p>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            {t('snapshots.confirmRestoreHint')}
          </p>
          {restoreMutation.isError && (
            <p className="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">
              {(restoreMutation.error as Error).message}
            </p>
          )}
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setRestoreTarget(null)}>
            {t('common.cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={restoreMutation.isPending}
            onClick={() => restoreTarget && restoreMutation.mutate(restoreTarget.name)}
          >
            {t('snapshots.restore')}
          </Button>
        </div>
      </Dialog>

      {/* Delete confirm */}
      <Dialog open={deleteTarget !== null} onClose={() => setDeleteTarget(null)} title={t('snapshots.confirmDeleteTitle')} size="small">
        <div className="py-2">
          <p>{t('snapshots.confirmDelete', { name: deleteTarget?.name ?? '' })}</p>
          {deleteMutation.isError && (
            <p className="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">
              {(deleteMutation.error as Error).message}
            </p>
          )}
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setDeleteTarget(null)}>
            {t('common.cancel')}
          </Button>
          <Button
            variant="danger"
            disabled={deleteMutation.isPending}
            onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget.name)}
          >
            {t('snapshots.delete')}
          </Button>
        </div>
      </Dialog>

      {/* Diff summary after restore */}
      {diffCount !== null && (
        <p className="text-sm text-green-600 dark:text-green-400" role="status">
          {t('snapshots.loaded', { count: diffCount })}
        </p>
      )}
    </div>
  )
}

export default Snapshots
