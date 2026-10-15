import { apiClient } from './client'

export interface DiffResponse {
  count: number
  changes: Array<{
    path: string
    old?: any
    new?: any
  }>
}

export interface RevisionInfo {
  id: number
  timestamp: string
  user: string
  comment: string
}

export const getConfigDiff = async (): Promise<DiffResponse> => {
  return apiClient<DiffResponse>('/api/v1/config/diff')
}

export const getRevisions = async (): Promise<{ revisions: RevisionInfo[] }> => {
  return apiClient<{ revisions: RevisionInfo[] }>('/api/v1/revisions')
}
export interface CommitResponse {
  revision: number
  confirm_deadline?: string
}

// commitConfig applies the candidate (POST /api/v1/commit). A validation
// failure throws an APIRequestError whose details list {path, message}.
export const commitConfig = (comment = '', confirmMinutes = 0): Promise<CommitResponse> =>
  apiClient<CommitResponse>('/api/v1/commit', {
    method: 'POST',
    body: JSON.stringify({ comment, ...(confirmMinutes > 0 ? { confirm_minutes: confirmMinutes } : {}) }),
  })

// confirmCommit keeps a commit made with confirm_minutes (otherwise it rolls back).
export const confirmCommit = (): Promise<{ status: string }> =>
  apiClient<{ status: string }>('/api/v1/commit/confirm', { method: 'POST' })

export interface SystemStatus {
  version: string
  pending_confirm?: { deadline: string } | null
}

export const getSystemStatus = (): Promise<SystemStatus> => apiClient<SystemStatus>('/api/v1/system/status')

// rollbackRevisionTo re-applies an earlier revision (POST /api/v1/revisions/{id}/rollback).
export const rollbackRevisionTo = (id: number): Promise<CommitResponse> =>
  apiClient<CommitResponse>(`/api/v1/revisions/${id}/rollback`, { method: 'POST' })
