import React, { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { commitConfig, confirmCommit, getConfigDiff, getSystemStatus } from '../api/config'
import { APIRequestError } from '../api/client'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { Dialog } from '../components/Dialog'
import { DiffViewer } from '../components/DiffViewer'

interface FieldError {
  path: string
  message: string
}

// fieldErrors extracts the validation errors of a failed commit.
function fieldErrors(e: unknown): FieldError[] {
  if (e instanceof APIRequestError && Array.isArray(e.details)) {
    return (e.details as FieldError[]).filter((d) => d && typeof d.message === 'string')
  }
  return []
}

// CommitBar shows the number of uncommitted changes, opens the review dialog
// and commits the candidate; a commit waiting for confirmation gets a banner
// with a Confirm button (POST /api/v1/commit/confirm).
export const CommitBar: React.FC = () => {
  const [open, setOpen] = useState(false)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [errors, setErrors] = useState<FieldError[]>([])
  const [done, setDone] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const { data: diffData, refetch, isLoading } = useQuery({
    queryKey: ['configDiff'],
    queryFn: getConfigDiff,
    refetchInterval: 10000,
  })
  const { data: status, refetch: refetchStatus } = useQuery({
    queryKey: ['systemStatus', 'commitbar'],
    queryFn: getSystemStatus,
    refetchInterval: 5000,
  })
  const pendingConfirm = !!status?.pending_confirm

  const openDialog = () => {
    setError(null)
    setErrors([])
    setDone(null)
    setOpen(true)
  }

  const handleCommit = async () => {
    setBusy(true)
    setError(null)
    setErrors([])
    try {
      const res = await commitConfig(comment.trim())
      setComment('')
      setOpen(false)
      setDone(`Committed revision ${res.revision}`)
      // everything shown may have changed (running config, status, lists)
      await queryClient.invalidateQueries()
      refetch()
    } catch (e) {
      setError((e as Error).message)
      setErrors(fieldErrors(e))
    } finally {
      setBusy(false)
    }
  }

  const handleConfirm = async () => {
    setBusy(true)
    try {
      await confirmCommit()
      setDone('Commit confirmed')
      refetchStatus()
    } catch (e) {
      setDone((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const changesCount = diffData?.count || 0

  return (
    <div className="flex items-center space-x-4">
      {pendingConfirm && (
        <div className="bg-yellow-500 text-white px-4 py-2 rounded-lg flex items-center space-x-2" role="status">
          <span>Commit pending confirmation</span>
          <Button variant="secondary" size="sm" onClick={handleConfirm} disabled={busy}>
            Confirm
          </Button>
        </div>
      )}
      {done && !open && (
        <span className="text-sm text-green-600 dark:text-green-400" role="status">
          {done}
        </span>
      )}

      <div className="flex items-center space-x-2">
        <Button variant="secondary" size="sm" onClick={openDialog} disabled={changesCount === 0}>
          <Badge variant="secondary">{changesCount}</Badge>
          <span className="ml-1">Changes</span>
        </Button>
      </div>

      <Dialog open={open} onClose={() => setOpen(false)} title="Review Changes" size="large">
        <div className="max-h-96 overflow-y-auto">
          {isLoading ? (
            <div className="text-center py-8">Loading changes...</div>
          ) : diffData?.changes && diffData.changes.length > 0 ? (
            <DiffViewer changes={diffData.changes} />
          ) : (
            <div className="text-center py-8 text-gray-500">No changes to review</div>
          )}
        </div>
        {error && (
          <div className="mt-4 rounded bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-300" role="alert">
            <p className="font-medium">{error}</p>
            {errors.length > 0 && (
              <ul className="mt-2 list-disc pl-5">
                {errors.map((fe) => (
                  <li key={fe.path + fe.message}>
                    <code>{fe.path}</code>: {fe.message}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
        <label className="mt-4 block text-sm text-gray-700 dark:text-gray-300">
          Comment (optional)
          <input
            aria-label="Commit comment"
            className="mt-1 block w-full rounded border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-900"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            maxLength={200}
          />
        </label>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setOpen(false)}>
            Close
          </Button>
          <Button variant="primary" onClick={handleCommit} disabled={changesCount === 0 || busy}>
            {busy ? 'Committing…' : 'Commit'}
          </Button>
        </div>
      </Dialog>
    </div>
  )
}
