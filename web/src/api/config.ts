import { apiClient } from './client'

export interface DiffResponse {
  count: number
  changes: Array<{
    path: string
    old?: any
    new?: any
  }>
}

// One saved config (GET /api/v1/revisions lists them newest first; the
// first entry is the running config).
export interface RevisionInfo {
  revision: number
  time: string // RFC 3339, UTC
  user: string
  comment?: string
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

// Live state of one smart-queue (SQM) deployment on a WAN.
export interface SQMStatus {
  interface: string
  active: boolean
  download_mbit: number
  upload_mbit: number
}

export interface SystemStatus {
  version: string
  pending_confirm?: { deadline: string } | null
  sqm?: SQMStatus[]
  // a Debian update (kernel, libc) waits for a restart
  reboot_required?: boolean
  reboot_packages?: string[] | null
  // the public demo (goblind --demo): nothing is real, it resets every hour
  demo?: boolean
}

export const getSystemStatus = (): Promise<SystemStatus> => apiClient<SystemStatus>('/api/v1/system/status')

// rollbackRevisionTo re-applies an earlier revision (POST /api/v1/revisions/{id}/rollback).
export const rollbackRevisionTo = (id: number): Promise<CommitResponse> =>
  apiClient<CommitResponse>(`/api/v1/revisions/${id}/rollback`, { method: 'POST' })
