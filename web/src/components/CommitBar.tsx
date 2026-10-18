import React, { useState, useEffect, useRef } from 'react'
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

// RISKY_PREFIXES are diff paths that can cut off access to the firewall.
const RISKY_PREFIXES = ['interfaces', 'vlans', 'policies', 'system.admin_port', 'vpn', 'routing']

function isRiskyPath(path: string): boolean {
  return RISKY_PREFIXES.some((p) => path.startsWith(p))
}

// formatRemaining converts milliseconds to "m:ss" or "ss".
function formatRemaining(ms: number): string {
  const totalSec = Math.max(0, Math.ceil(ms / 1000))
  const m = Math.floor(totalSec / 60)
  const s = totalSec % 60
  return m > 0 ? `${m}:${String(s).padStart(2, '0')}` : `${s}s`
}

// CommitBar shows the number of uncommitted changes, opens the review dialog
// and commits the candidate; a commit waiting for confirmation gets a banner
// with a Confirm button (POST /api/v1/commit/confirm) and a countdown.
export const CommitBar: React.FC = () => {
  const [open, setOpen] = useState(false)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [errors, setErrors] = useState<FieldError[]>([])
  const [done, setDone] = useState<string | null>(null)
  const [autoRollback, setAutoRollback] = useState(false)
  const [minutes, setMinutes] = useState(5)
  const queryClient = useQueryClient()

  // Track previous pending_confirm deadline to detect rollback.
  const prevDeadlineRef = useRef<string | null>(null)

  const { data: diffData, refetch, isLoading } = useQuery({
    queryKey: ['configDiff'],
    queryFn: getConfigDiff,
    refetchInterval: 10000,
  })
  const { data: status, refetch: refetchStatus } = useQuery({
    queryKey: ['systemStatus', 'commitbar'],
    queryFn: getSystemStatus,
    refetchInterval: 1000,
  })

  // Countdown state for the pending-confirm banner.
  const [now, setNow] = useState(() => Date.now())
  const pendingConfirm = !!status?.pending_confirm
  const deadline = status?.pending_confirm?.deadline ?? null

  useEffect(() => {
    if (!pendingConfirm) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [pendingConfirm])

  // Detect when the deadline has passed without confirmation → rollback happened.
  useEffect(() => {
    if (deadline && prevDeadlineRef.current && deadline !== prevDeadlineRef.current) {
      // Deadline changed means a new commit started; reset.
      setDone(null)
    }
    if (!deadline && prevDeadlineRef.current) {
      // pending_confirm went away: either confirmed or rolled back.
      // If we didn't confirm (no busy state change), it was rolled back.
      setDone('The commit was rolled back (not confirmed in time)')
      queryClient.invalidateQueries()
      refetch()
    }
    prevDeadlineRef.current = deadline
  }, [deadline, queryClient, refetch])

  const openDialog = () => {
    setError(null)
    setErrors([])
    setDone(null)
    // Pre-check auto-rollback if the diff touches risky paths.
    const risky = (diffData?.changes ?? []).some((c) => isRiskyPath(c.path))
    setAutoRollback(risky)
    setOpen(true)
  }

  const handleCommit = async () => {
    setBusy(true)
    setError(null)
    setErrors([])
    try {
      const res = await commitConfig(comment.trim(), autoRollback ? minutes : 0)
      setComment('')
      setOpen(false)
      setDone(`Committed revision ${res.revision}`)
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

  // Compute remaining time for the countdown.
  let remainingMs: number | null = null
  if (deadline) {
    remainingMs = new Date(deadline).getTime() - now
  }
  const isUrgent = remainingMs !== null && remainingMs <= 60_000

  return (
    <div className="flex items-center space-x-4">
      {pendingConfirm && (
        <div
          className={`${isUrgent ? 'bg-red-600' : 'bg-yellow-500'} text-white px-4 py-2 rounded-lg flex items-center space-x-3`}
          role="status"
        >
          <span>Commit pending confirmation</span>
          {remainingMs !== null && remainingMs > 0 && (
            <span className="font-mono text-sm">{formatRemaining(remainingMs)}</span>
          )}
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

        <div className="mt-4 flex items-center space-x-3">
          <input
            type="checkbox"
            id="auto-rollback"
            checked={autoRollback}
            onChange={(e) => setAutoRollback(e.target.checked)}
            className="rounded border-gray-300 dark:border-gray-600"
          />
          <label htmlFor="auto-rollback" className="text-sm text-gray-700 dark:text-gray-300">
            Roll back automatically unless I confirm within
          </label>
          <input
            type="number"
            aria-label="Rollback minutes"
            min={1}
            max={60}
            value={minutes}
            onChange={(e) => {
              const v = e.target.value === '' ? undefined : Number(e.target.value)
              if (v !== undefined && v >= 1 && v <= 60) setMinutes(v)
            }}
            className="w-16 rounded border border-gray-300 bg-white px-2 py-1 text-sm dark:border-gray-600 dark:bg-gray-900"
          />
          <span className="text-sm text-gray-700 dark:text-gray-300">minutes</span>
        </div>
        {autoRollback && (diffData?.changes ?? []).some((c) => isRiskyPath(c.path)) && (
          <p className="mt-1 text-xs text-yellow-600 dark:text-yellow-400">
            This change touches network access settings — auto-rollback is recommended.
          </p>
        )}

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
