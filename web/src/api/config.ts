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