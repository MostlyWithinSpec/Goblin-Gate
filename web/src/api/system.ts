// System API: admins, API tokens, backup/restore and system info.
// Types mirror internal/api/system_handlers.go and auth/tokens.go by JSON name.
import { apiClient } from './client'
import { csrfToken } from './auth'

export interface AdminInfo {
  username: string
  role: 'admin' | 'readonly'
  has_password: boolean
  totp_enabled?: boolean
}

export interface TokenInfo {
  id: string
  name: string
  user: string
  role: string
  created: string
  last_used?: string
}

// --- Admins -----------------------------------------------------------------

export const getAdmins = () =>
  apiClient<{ admins: AdminInfo[] }>('/api/v1/admins').then((r) => r.admins ?? [])

export function createAdmin(body: { username: string; role: string; password: string }): Promise<AdminInfo> {
  return apiClient<{ admin: AdminInfo }>('/api/v1/admins', {
    method: 'POST',
    body: JSON.stringify(body),
  }).then((r) => r.admin)
}

export const updateAdmin = (name: string, body: { role?: string; password?: string }) =>
  apiClient<{ status: string }>(`/api/v1/admins/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })

export const deleteAdmin = (name: string) =>
  apiClient<{ status: string }>(`/api/v1/admins/${encodeURIComponent(name)}`, { method: 'DELETE' })

// --- API tokens ---------------------------------------------------------------

export const getTokens = () =>
  apiClient<{ tokens: TokenInfo[] }>('/api/v1/auth/tokens').then((r) => r.tokens ?? [])

export function createToken(body: { name: string; readonly?: boolean }): Promise<{ token: string; info: TokenInfo }> {
  return apiClient<{ token: string; info: TokenInfo }>('/api/v1/auth/tokens', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export const deleteToken = (id: string) =>
  apiClient<{ status: string }>(`/api/v1/auth/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- Active sessions ------------------------------------------------------------

export interface SessionInfo {
  id: string
  user: string
  role: string
  addr: string
  user_agent: string
  created: string
  last_seen: string
  current?: boolean
}

export const getSessionList = () =>
  apiClient<{ sessions: SessionInfo[] }>('/api/v1/auth/sessions').then((r) => r.sessions ?? [])

export const deleteSession = (id: string) =>
  apiClient(`/api/v1/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- Backup / restore ---------------------------------------------------------

export async function downloadBackup(): Promise<void> {
  const res = await fetch('/api/v1/system/backup', { credentials: 'same-origin' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `goblin-gate-backup-${new Date().toISOString().slice(0, 10)}.zip`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

export function restoreBackup(file: File): Promise<{ status: string; hostname?: string; message?: string }> {
  const form = new FormData()
  form.append('backup', file)
  return fetch('/api/v1/system/restore', {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': csrfToken() },
  }).then(async (res) => {
    if (!res.ok) {
      let message = `HTTP ${res.status}`
      try {
        const body = await res.json()
        if (body?.error?.message) message = body.error.message
      } catch { /* not JSON */ }
      throw new Error(message)
    }
    return res.json() as Promise<{ status: string; hostname?: string; message?: string }>
  })
}

// --- Import from pfSense --------------------------------------------------------

export interface ImportReportEntry {
  section: string
  item: string
  status: 'converted' | 'approximated' | 'skipped'
  detail?: string
  risk?: boolean
}

export interface ImportedInterface {
  pfsense: string
  pfsense_device: string
  description?: string
  name: string
  role: string
  vlan?: boolean
  device: string
}

export interface ImportResult {
  report: ImportReportEntry[]
  interfaces: ImportedInterface[]
  summary: { converted: number; approximated: number; skipped: number }
  validation_errors?: { path: string; message: string }[]
}

export function importPfSense(file: File, devices?: Record<string, string>): Promise<ImportResult> {
  const form = new FormData()
  form.append('config', file)
  if (devices && Object.keys(devices).length > 0) {
    form.append('devices', JSON.stringify(devices))
  }
  return fetch('/api/v1/import/pfsense', {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': csrfToken() },
  }).then(async (res) => {
    if (!res.ok) {
      let message = `HTTP ${res.status}`
      try {
        const body = await res.json()
        if (body?.error?.message) message = body.error.message
      } catch { /* not JSON */ }
      throw new Error(message)
    }
    return res.json() as Promise<ImportResult>
  })
}

// --- System info ----------------------------------------------------------------

export interface SystemInfo {
  version: string
  time: string
  hostname?: string
  kernel?: string
  uptime_seconds?: number
  timezone?: string
  ntp_synchronized?: boolean
}

export const getSystemInfo = () => apiClient<SystemInfo>('/api/v1/system/info')

// --- Updates ------------------------------------------------------------------
// Mirrors the /api/v1/system/updates handlers added in task 051.

export interface UpdateLast {
  state: 'running' | 'done' | 'rolled_back' | 'failed'
  from?: string
  to?: string
  include_os?: boolean
  step?: string
  message?: string
  started?: string
  finished?: string
}

export interface UpdatesStatus {
  installed: string
  available?: string
  release_url?: string
  os_updates?: number
  checked?: string // time of the last check; absent = never checked
  check_error?: string
  last?: UpdateLast
}

export const getUpdates = (refresh = false) =>
  apiClient<UpdatesStatus>(`/api/v1/system/updates${refresh ? '?refresh=1' : ''}`)

export function applyUpdate(body: { version?: string; include_os?: boolean }): Promise<{ status: string }> {
  return apiClient<{ status: string }>('/api/v1/system/updates/apply', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

// --- Generated config viewer (task 074 backend) ---------------------------------

export type GeneratedConfig = 'running' | 'candidate'

export interface GeneratedFile {
  path: string
  renderer: string
  size: number
  redacted: boolean
}

export interface GeneratedListResponse {
  files: GeneratedFile[]
}

export interface GeneratedFileContent {
  path: string
  renderer: string
  content: string
  redacted: boolean
  truncated: boolean
}

export interface GeneratedDiffResponse {
  path: string
  diff: string
}

const cfgQuery = (config?: GeneratedConfig) => (config ? `?config=${config}` : '')

export const getGeneratedFiles = (config?: GeneratedConfig) =>
  apiClient<GeneratedListResponse>(`/api/v1/system/generated${cfgQuery(config)}`).then((r) => r.files ?? [])

export const getGeneratedFile = (path: string, config?: GeneratedConfig) =>
  apiClient<GeneratedFileContent>(
    `/api/v1/system/generated/file?path=${encodeURIComponent(path)}${config ? `&config=${config}` : ''}`,
  )

export const getGeneratedDiff = (path: string) =>
  apiClient<GeneratedDiffResponse>(`/api/v1/system/generated/diff?path=${encodeURIComponent(path)}`)
