import { apiClient } from './client'

// SnapshotInfo is the summary form returned by list endpoints (no config body).
export interface SnapshotInfo {
  name: string
  comment?: string
  created: string // RFC 3339, UTC
  created_by: string
  revision: number // running revision it was taken from; 0 for candidate
}

// FullSnapshot adds the config body (GET /api/v1/snapshots/{name}).
export interface FullSnapshot extends SnapshotInfo {
  config: unknown
}

// Save a snapshot of the given config ("running" or "candidate").
export const saveSnapshot = (name: string, comment: string, from: 'running' | 'candidate' = 'running'): Promise<SnapshotInfo> =>
  apiClient<SnapshotInfo>('/api/v1/snapshots', {
    method: 'POST',
    body: JSON.stringify({ name, comment, from }),
  })

export const listSnapshots = async (): Promise<SnapshotInfo[]> => {
  const r = await apiClient<{ snapshots: SnapshotInfo[] }>('/api/v1/snapshots')
  return r.snapshots ?? []
}

export const getSnapshot = (name: string): Promise<FullSnapshot> =>
  apiClient<FullSnapshot>(`/api/v1/snapshots/${encodeURIComponent(name)}`)

// Restore loads the snapshot into the candidate (never commits).
export const restoreSnapshot = (name: string): Promise<{ status: string; changes: number }> =>
  apiClient<{ status: string; changes: number }>(`/api/v1/snapshots/${encodeURIComponent(name)}/restore`, {
    method: 'POST',
  })

// DELETE answers 204 (apiClient returns undefined for it).
export const deleteSnapshot = async (name: string): Promise<void> => {
  await apiClient<void>(`/api/v1/snapshots/${encodeURIComponent(name)}`, { method: 'DELETE' })
}
