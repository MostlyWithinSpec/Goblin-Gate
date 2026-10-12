import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { Dialog } from '../components/Dialog'
import { DiffViewer } from '../components/DiffViewer'
import { getRevisions } from '../api/config'

// "Oct 11, 2026, 14:05" in the viewer's locale (date-fns is not an allowed dependency).
const formatTime = (ts: string) =>
  new Date(ts).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

interface RevisionInfo {
  id: number
  timestamp: string
  user: string
  comment: string
}

export const Revisions: React.FC = () => {
  const [selectedRevision, setSelectedRevision] = useState<number | null>(null)
  const [showRevisionModal, setShowRevisionModal] = useState(false)
  const [showRollbackModal, setShowRollbackModal] = useState(false)
  const [rollbackRevision, setRollbackRevision] = useState<number | null>(null)
  const queryClient = useQueryClient()
  
  const { data: revisions, isLoading } = useQuery({
    queryKey: ['revisions'],
    queryFn: getRevisions
  })
  
  const { data: diffData } = useQuery({
    queryKey: ['revisionDiff', selectedRevision],
    queryFn: async () => {
      if (!selectedRevision) return null
      // We need to implement a way to get diff against running config for a specific revision
      // For now, we'll just return a placeholder
      return { count: 0, changes: [] }
    },
    enabled: !!selectedRevision
  })
  
  const rollbackMutation = useMutation({
    mutationFn: async (revisionId: number) => {
      const response = await fetch(`/api/v1/revisions/${revisionId}/rollback`, {
        method: 'POST'
      })
      if (!response.ok) {
        throw new Error('Failed to rollback')
      }
      return response.json()
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['revisions'] })
      setShowRollbackModal(false)
      setRollbackRevision(null)
    }
  })
  
  const handleRollback = (revisionId: number) => {
    setRollbackRevision(revisionId)
    setShowRollbackModal(true)
  }
  
  const confirmRollback = () => {
    if (rollbackRevision) {
      rollbackMutation.mutate(rollbackRevision)
    }
  }
  
  const handleViewRevision = (revisionId: number) => {
    setSelectedRevision(revisionId)
    setShowRevisionModal(true)
  }
  
  if (isLoading) {
    return <div className="p-4">Loading revisions...</div>
  }
  
  return (
    <div className="space-y-4">
      <div className="flex justify-between items-center">
        <h1 className="text-2xl font-bold">Revisions</h1>
        <Button variant="primary" onClick={() => {}}>
          New Revision
        </Button>
      </div>
      
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
        <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
          <thead className="bg-gray-50 dark:bg-gray-700">
            <tr>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider">Revision</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider">Date</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider">User</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider">Comment</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-300 uppercase tracking-wider">Actions</th>
            </tr>
          </thead>
          <tbody className="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
            {revisions?.revisions?.map((revision: RevisionInfo) => (
              <tr key={revision.id} className="hover:bg-gray-50 dark:hover:bg-gray-700">
                <td className="px-6 py-4 whitespace-nowrap">
                  <Badge variant="secondary">{revision.id}</Badge>
                </td>
                <td className="px-6 py-4 whitespace-nowrap">
                  {formatTime(revision.timestamp)}
                </td>
                <td className="px-6 py-4 whitespace-nowrap">
                  {revision.user}
                </td>
                <td className="px-6 py-4">
                  {revision.comment}
                </td>
                <td className="px-6 py-4 whitespace-nowrap text-sm font-medium">
                  <Button 
                    variant="secondary" 
                    size="sm" 
                    onClick={() => handleViewRevision(revision.id)}
                    className="mr-2"
                  >
                    View
                  </Button>
                  <Button 
                    variant="secondary" 
                    size="sm" 
                    onClick={() => handleRollback(revision.id)}
                    disabled={revision.id === 1} // Can't rollback to first revision
                  >
                    Rollback
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        
        {(!revisions || revisions.revisions.length === 0) && (
          <div className="text-center py-8 text-gray-500">
            No revisions found
          </div>
        )}
      </div>
      
      {/* Revision details modal */}
      <Dialog 
        open={showRevisionModal} 
        onClose={() => setShowRevisionModal(false)}
        title={`Revision ${selectedRevision}`}
        size="large"
      >
        <div className="max-h-96 overflow-y-auto">
          {isLoading ? (
            <div className="text-center py-8">Loading...</div>
          ) : (
            <div>
              <div className="grid grid-cols-2 gap-4 mb-6">
                <div>
                  <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400">Revision ID</h3>
                  <p className="mt-1">{selectedRevision}</p>
                </div>
                <div>
                  <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400">Date</h3>
                  <p className="mt-1">
                    {revisions?.revisions.find(r => r.id === selectedRevision)?.timestamp ? 
                      formatTime(revisions.revisions.find(r => r.id === selectedRevision)!.timestamp) : 
                      'Unknown'}
                  </p>
                </div>
              </div>
              <div>
                <h3 className="text-sm font-medium text-gray-500 dark:text-gray-400 mb-2">Changes</h3>
                {diffData?.changes && diffData.changes.length > 0 ? (
                  <DiffViewer changes={diffData.changes} />
                ) : (
                  <div className="text-center py-4 text-gray-500">No changes in this revision</div>
                )}
              </div>
            </div>
          )}
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setShowRevisionModal(false)}>
            Close
          </Button>
        </div>
      </Dialog>
      
      {/* Rollback confirmation modal */}
      <Dialog 
        open={showRollbackModal} 
        onClose={() => setShowRollbackModal(false)}
        title="Confirm Rollback"
      >
        <div className="py-4">
          <p>Are you sure you want to rollback to revision #{rollbackRevision}?</p>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            This action will restore the configuration to the state at that revision.
          </p>
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setShowRollbackModal(false)}>
            Cancel
          </Button>
          <Button variant="danger" onClick={confirmRollback}>
            Rollback
          </Button>
        </div>
      </Dialog>
    </div>
  )
}