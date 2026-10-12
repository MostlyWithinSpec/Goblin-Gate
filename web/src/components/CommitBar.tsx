import React, { useEffect, useState } from 'react'
import { getConfigDiff } from '../api/config'
import { useQuery } from '@tanstack/react-query'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { Dialog } from '../components/Dialog'
import { DiffViewer } from '../components/DiffViewer'
import { useNavigate } from 'react-router-dom'

export const CommitBar: React.FC = () => {
  const [showDiffModal, setShowDiffModal] = useState(false)
  const [pendingConfirm, setPendingConfirm] = useState(false)
  const navigate = useNavigate()
  
  const { data: diffData, refetch, isLoading } = useQuery({
    queryKey: ['configDiff'],
    queryFn: getConfigDiff,
    refetchInterval: 10000, // Poll every 10 seconds
  })
  
  // Check for pending confirm
  useEffect(() => {
    const checkPendingConfirm = async () => {
      try {
        const response = await fetch('/api/v1/system/status')
        const status = await response.json()
        setPendingConfirm(!!status.pending_confirm)
      } catch (error) {
        // Ignore error, we'll just not show the confirm banner
      }
    }
    
    checkPendingConfirm()
    const interval = setInterval(checkPendingConfirm, 5000) // Check every 5 seconds
    
    return () => clearInterval(interval)
  }, [])
  
  const handleCommit = async () => {
    try {
      const response = await fetch('/api/v1/commit', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({})
      })
      
      if (response.ok) {
        // Refetch diff to clear it
        refetch()
        navigate('/')
      }
    } catch (error) {
      console.error('Failed to commit:', error)
    }
  }
  
  const handleReviewChanges = () => {
    setShowDiffModal(true)
  }
  
  const handleConfirmPending = () => {
    setShowDiffModal(true)
  }
  
  // Calculate the number of changes
  const changesCount = diffData?.count || 0
  
  return (
    <div className="flex items-center space-x-4">
      {/* Pending confirm banner */}
      {pendingConfirm && (
        <div className="bg-yellow-500 text-white px-4 py-2 rounded-lg flex items-center space-x-2">
          <span>Commit pending confirmation</span>
          <Button 
            variant="secondary" 
            size="sm"
            onClick={handleConfirmPending}
          >
            Confirm Changes
          </Button>
        </div>
      )}
      
      {/* Commit bar with diff count */}
      <div className="flex items-center space-x-2">
        <Button 
          variant="secondary" 
          size="sm"
          onClick={handleReviewChanges}
          disabled={changesCount === 0}
        >
          <Badge variant="secondary">{changesCount}</Badge>
          <span className="ml-1">Changes</span>
        </Button>
      </div>
      
      {/* Diff modal */}
      <Dialog 
        open={showDiffModal} 
        onClose={() => setShowDiffModal(false)}
        title="Review Changes"
        size="large"
      >
        <div className="max-h-96 overflow-y-auto">
          {isLoading ? (
            <div className="text-center py-8">Loading changes...</div>
          ) : diffData?.changes && diffData.changes.length > 0 ? (
            <DiffViewer changes={diffData.changes} />
          ) : (
            <div className="text-center py-8 text-gray-500">No changes to review</div>
          )}
        </div>
        <div className="flex justify-end space-x-2 mt-4">
          <Button variant="secondary" onClick={() => setShowDiffModal(false)}>
            Close
          </Button>
          <Button 
            variant="primary" 
            onClick={handleCommit}
            disabled={changesCount === 0}
          >
            Commit
          </Button>
        </div>
      </Dialog>
    </div>
  )
}