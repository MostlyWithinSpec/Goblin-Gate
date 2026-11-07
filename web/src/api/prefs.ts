// Per-admin UI preferences (GET/PUT /api/v1/ui/prefs, stored per user on
// the box like the dashboards).
import { apiClient } from './client'

export interface Prefs {
  language?: string // a code from i18n languages(); absent = the browser's
}

export function getPrefs(): Promise<Prefs> {
  return apiClient<Prefs>('/api/v1/ui/prefs')
}

export function savePrefs(p: Prefs): Promise<{ status: string }> {
  return apiClient('/api/v1/ui/prefs', { method: 'PUT', body: JSON.stringify(p) })
}
