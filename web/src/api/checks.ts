// Goblin Checks API (task 109): the audit of the running config plus a
// little live state. Types mirror internal/checks/checks.go by JSON name.
import { apiClient } from './client'

export type Severity = 'high' | 'medium' | 'low' | 'info'

export interface Finding {
  // key identifies the finding across runs ("<check>:<subject>"); it is what
  // a dismissal stores. It contains ":", so URL-encode it.
  key: string
  check: string
  severity: Severity
  title: string
  detail: string
  paths?: string[]
  // link is the UI path where the finding is fixed.
  link: string
}

export interface Passed {
  check: string
  title: string
}

export interface Report {
  score: number
  findings: Finding[]
  dismissed: Finding[]
  passed: Passed[]
}

export const getChecks = () => apiClient<Report>('/api/v1/checks')

// dismiss hides a finding from the list and the score until it is restored.
// Dismissals are box state, not config, so nothing needs a commit.
export const dismissFinding = (key: string) =>
  apiClient<{ status: string }>('/api/v1/checks/dismiss', { method: 'POST', body: JSON.stringify({ key }) })

export const restoreFinding = (key: string) =>
  apiClient<{ status: string }>(`/api/v1/checks/dismiss/${encodeURIComponent(key)}`, { method: 'DELETE' })

// severities is the order findings are shown in, most important first.
export const SEVERITIES: Severity[] = ['high', 'medium', 'low', 'info']

export const SEVERITY_LABELS: Record<Severity, string> = {
  high: 'High',
  medium: 'Medium',
  low: 'Low',
  info: 'Info',
}

// countBySeverity counts findings per severity; the widget needs the high one.
export function countBySeverity(findings: Finding[] | undefined, severity: Severity): number {
  return (findings ?? []).filter((f) => f.severity === severity).length
}

// groupBySeverity buckets findings under every severity in order, dropping
// the empty ones. The server already sorts findings high → info.
export function groupBySeverity(findings: Finding[] | undefined): { severity: Severity; findings: Finding[] }[] {
  const out: { severity: Severity; findings: Finding[] }[] = []
  for (const severity of SEVERITIES) {
    const list = (findings ?? []).filter((f) => f.severity === severity)
    if (list.length > 0) out.push({ severity, findings: list })
  }
  return out
}

// scoreTone picks the colours for a score: green at the top, amber in the
// middle, red when there is real damage.
export function scoreTone(score: number): 'success' | 'warning' | 'error' {
  if (score >= 80) return 'success'
  if (score >= 50) return 'warning'
  return 'error'
}
