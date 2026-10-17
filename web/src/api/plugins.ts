import { apiClient, APIRequestError } from './client'

// Plugin schema types mirror internal/plugins/manifest.go (JSON Schema subset).
export interface PluginPage {
  path: string
  title: string
}

export interface PluginUI {
  pages?: PluginPage[]
}

export interface PluginProperty {
  type: 'string' | 'integer' | 'number' | 'boolean' | 'array'
  description?: string
  enum?: unknown[]
  minimum?: number
  maximum?: number
  maxLength?: number
  default?: unknown
  items?: { type: string }
}

export interface PluginSchema {
  type: string
  properties?: Record<string, PluginProperty>
  required?: string[]
}

// PluginStatus mirrors internal/plugins/manager.go Status.
export interface PluginStatus {
  name: string
  version?: string
  description?: string
  requested_capabilities: string[]
  capabilities: string[]
  enabled: boolean
  settings?: Record<string, unknown>
  settings_schema?: PluginSchema | null
  ui?: PluginUI | null
  state: 'running' | 'stopped' | 'failed' | 'invalid' | 'not_installed'
  problem?: string
}

export interface PluginsResponse {
  plugins: PluginStatus[]
}

export interface SaveResult {
  status: string
  validation_errors: unknown[] | null
}

// The six capabilities a plugin may ask for (docs/dev/plugins.md).
export const CAPABILITY_DESCRIPTIONS: Record<string, string> = {
  'events:read': 'Read the event log: traffic, IPS, DNS and system events.',
  'stats:read': 'Read statistics and run stats queries.',
  'config:read': 'Read the running configuration.',
  'notify:send': 'Send an alert through your notification rules.',
  'blocks:read': 'Read temporary blocks.',
  'blocks:write': 'Add and remove temporary blocks.'
}

export function getPlugins(): Promise<PluginsResponse> {
  return apiClient<PluginsResponse>('/api/v1/plugins')
}

// Enable a plugin in the candidate with the given capabilities. Omit
// capabilities to grant everything it asks for.
export async function enablePlugin(
  name: string,
  capabilities?: string[]
): Promise<SaveResult> {
  try {
    return await apiClient<SaveResult>(`/api/v1/plugins/${encodeURIComponent(name)}/enable`, {
      method: 'POST',
      body: JSON.stringify({ capabilities })
    })
  } catch (err) {
    if (err instanceof APIRequestError && err.code === 'plugin_invalid') {
      throw new Error(err.message || 'This plugin cannot be enabled.')
    }
    throw err
  }
}

export async function disablePlugin(name: string): Promise<SaveResult> {
  return apiClient<SaveResult>(`/api/v1/plugins/${encodeURIComponent(name)}/disable`, {
    method: 'POST',
    body: JSON.stringify({})
  })
}

// Save a plugin's settings in the candidate. On a 400 validation_failed the
// thrown error carries the field errors in .details (array of "key: message").
export async function setPluginSettings(
  name: string,
  settings: Record<string, unknown>
): Promise<SaveResult> {
  return apiClient<SaveResult>(`/api/v1/plugins/${encodeURIComponent(name)}/settings`, {
    method: 'PUT',
    body: JSON.stringify({ settings })
  })
}
