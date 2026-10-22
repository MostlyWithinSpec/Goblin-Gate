// Templates API (task 111): a template answers a few questions and writes the
// result into the candidate. Types mirror internal/templates/templates.go and
// internal/api/templates_handlers.go by JSON name.
import { apiClient } from './client'

// TemplateInput is one question. The type picks the widget; options only come
// with interface, tunnel and profile.
export interface TemplateInput {
  name: string
  label: string
  type: TemplateInputType
  help?: string
  default?: string
  required?: boolean
  options?: string[]
}

// TemplateInputType is the widget the answer gets. The list mirrors the
// Type* constants in internal/templates/templates.go.
export type TemplateInputType =
  | 'name'
  | 'interface'
  | 'tunnel'
  | 'profile'
  | 'vlan_id'
  | 'cidr'
  | 'ip'
  | 'ips'
  | 'port'
  | 'protocol'
  | 'sources'
  | 'days'
  | 'time'
  | 'bool'

export interface TemplateInfo {
  id: string
  version: number
  title: string
  description: string
  inputs: TemplateInput[]
  // unavailable says why the template cannot be used with this config; absent
  // when it can.
  unavailable?: string
}

// TemplateChange is one item of the diff a template would make — the same
// shape as a change of GET /api/v1/config/diff.
export interface TemplateChange {
  path: string
  op?: string
  old?: unknown
  new?: unknown
}

export interface InputError {
  input: string
  message: string
}

export interface ValidationError {
  path: string
  message: string
}

// TemplateResult is what preview answers and what apply reports in
// error.details when the answers need fixing.
export interface TemplateResult {
  changes: TemplateChange[]
  input_errors: InputError[]
  validation_errors: ValidationError[]
}

export const getTemplates = (): Promise<TemplateInfo[]> =>
  apiClient<{ templates: TemplateInfo[] }>('/api/v1/templates').then((r) => r.templates ?? [])

// previewTemplate answers with the diff and the errors; the candidate stays as it is.
export const previewTemplate = (
  id: string,
  values: Record<string, string>,
  signal?: AbortSignal
): Promise<TemplateResult> =>
  apiClient<TemplateResult>(`/api/v1/templates/${encodeURIComponent(id)}/preview`, {
    method: 'POST',
    body: JSON.stringify({ values }),
    signal,
  })

// applyTemplate saves the result to the candidate (it never commits). With a
// problem in the answers it throws an APIRequestError whose details hold the
// preview body.
export const applyTemplate = (
  id: string,
  values: Record<string, string>,
  signal?: AbortSignal
): Promise<{ status: string; changes: TemplateChange[] }> =>
  apiClient<{ status: string; changes: TemplateChange[] }>(`/api/v1/templates/${encodeURIComponent(id)}/apply`, {
    method: 'POST',
    body: JSON.stringify({ values }),
    signal,
  })
