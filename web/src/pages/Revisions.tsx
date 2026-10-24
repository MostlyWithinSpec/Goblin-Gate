import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { Dialog } from '../components/Dialog'
import { getRevisions, rollbackRevisionTo, RevisionInfo } from '../api/config'

// "Oct 11, 2026, 14:05" in the viewer's locale (date-fns is not an allowed dependency).
const formatTime = (ts: string) => {
  const d = new Date(ts)
  return isNaN(d.getTime())
    ? ts
    : d.toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

const th = 'px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider'

export const Revisions: React.FC = () => {
  const [rollbackTo, setRollbackTo] = useState<RevisionInfo | null>(null)
  const queryClient = useQueryClient()

  const { data, isLoading, error } = useQuery({ queryKey: ['revisions'], queryFn: getRevisions })
  const revisions = data?.revisions ?? []
  const running = revisions[0]?.revision

  const rollbackMutation = useMutation({
    mutationFn: (rev: number) => rollbackRevisionTo(rev),
    onSuccess: () => {
      // the running config changed: everything shown may be stale
      queryClient.invalidateQueries()
      setRollbackTo(null)
    },
  })

  if (isLoading) {
    return <div className="p-4">Loading revisions...</div>
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">Revisions</h1>
      <p className="text-sm text-gray-600 dark:text-gray-400">
        Every commit saves a revision. Rolling back applies an earlier revision as a new commit.
      </p>
      {error != null && (
        <p className="text-sm text-red-600 dark:text-red-400" role="alert">
          {(error as Error).message}
        </p>
      )}

      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-700">
            <tr>
              <th className={th}>Revision</th>
              <th className={th}>Date</th>
              <th className={th}>User</th>
              <th className={th}>Comment</th>
              <th className={th}>Actions</th>
            </tr>
          </thead>
          <tbody className="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
            {revisions.map((r) => (
              <tr key={r.revision} className="hover:bg-gray-50 dark:hover:bg-gray-700">
                <td className="px-6 py-4 whitespace-nowrap">
                  <Badge variant="secondary">{r.revision}</Badge>
                  {r.revision === running && (
                    <span className="ml-2">
                      <Badge variant="success">running</Badge>
                    </span>
                  )}
                </td>
                <td className="px-6 py-4 whitespace-nowrap">{formatTime(r.time)}</td>
                <td className="px-6 py-4 whitespace-nowrap">{r.user}</td>
                <td className="px-6 py-4">{r.comment}</td>
                <td className="px-6 py-4 whitespace-nowrap text-sm font-medium">
                  {r.revision !== running && (
                    <Button variant="secondary" size="sm" onClick={() => { rollbackMutation.reset(); setRollbackTo(r) }}>
                      Rollback
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {revisions.length === 0 && <div className="text-center py-8 text-gray-500">No revisions yet</div>}
      </div>

      <Dialog open={rollbackTo !== null} onClose={() => setRollbackTo(null)} title="Confirm Rollback">
        <div className="py-4">
          <p>Roll back to revision #{rollbackTo?.revision}?</p>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            The configuration from {rollbackTo ? formatTime(rollbackTo.time) : ''} is applied now and saved as a new revision.
            Uncommitted changes are kept as they are.
          </p>
          {rollbackMutation.isError && (
            <p className="mt-2 text-sm text-red-600 dark:text-red-400" role="alert">
              {(rollbackMutation.error as Error).message}
            </p>
          )}
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setRollbackTo(null)}>
            Cancel
          </Button>
          <Button
            variant="danger"
            onClick={() => rollbackTo && rollbackMutation.mutate(rollbackTo.revision)}
            disabled={rollbackMutation.isPending}
          >
            Rollback
          </Button>
        </div>
      </Dialog>
    </div>
  )
}
