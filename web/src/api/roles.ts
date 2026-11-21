// Admin roles (internal/api/roles_handlers.go, task 149). A role is a name
// plus the scopes it grants on top of reading everything; admins[].role is
// "admin", "readonly" or one of these names. Roles change right away, like
// admins — no commit.
import { apiClient } from './client'

export interface RoleInfo {
  name: string
  description: string
  scopes: string[]
  builtin: boolean
  // users: the admins that have this role.
  users: string[]
}

export interface ScopeInfo {
  id: string
  description: string
}

export interface RolesResponse {
  roles: RoleInfo[]
  scopes: ScopeInfo[]
}

export const getRoles = () =>
  apiClient<RolesResponse>('/api/v1/roles').then((r) => ({ roles: r.roles ?? [], scopes: r.scopes ?? [] }))

export function createRole(body: { name: string; description?: string; scopes: string[] }): Promise<RoleInfo> {
  return apiClient<{ role: RoleInfo }>('/api/v1/roles', { method: 'POST', body: JSON.stringify(body) }).then((r) => r.role)
}

export function updateRole(name: string, body: { description?: string; scopes?: string[] }): Promise<{ status: string }> {
  return apiClient<{ status: string }>(`/api/v1/roles/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })
}

export const deleteRole = (name: string) =>
  apiClient<{ status: string }>(`/api/v1/roles/${encodeURIComponent(name)}`, { method: 'DELETE' })
