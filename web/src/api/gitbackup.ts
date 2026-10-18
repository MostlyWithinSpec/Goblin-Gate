// Git config history (task 098/099). The box keeps the running config in a git
// repo and pushes it to a remote over SSH. Types mirror internal/gitbackup by
// JSON name.
import { apiClient } from './client'

export interface GitBackupStatus {
  enabled: boolean
  remote?: string
  branch?: string
  public_key?: string
  host_key?: string
  last_revision?: number
  pushed_revision?: number
  last_push?: string
  pending: boolean // committed revisions not pushed yet
  last_error?: string
}

export const getGitBackupStatus = () => apiClient<GitBackupStatus>('/api/v1/git-backup')

export const pushGitBackup = () => apiClient<{ status: string }>('/api/v1/git-backup/push', { method: 'POST' })

export const forgetGitBackupHostKey = () =>
  apiClient<{ status: string }>('/api/v1/git-backup/forget-host-key', { method: 'POST' })
