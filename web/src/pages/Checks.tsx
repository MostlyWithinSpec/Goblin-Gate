// Goblin Checks (task 109): what to fix on this box, most important first.
// The score and the findings come from GET /api/v1/checks; a finding links to
// the page that fixes it and can be dismissed (box state, not config, so no
// commit and no change count).
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertOctagon, AlertTriangle, CheckCircle2, ChevronDown, ChevronRight, Info, ShieldCheck } from 'lucide-react'
import { Button } from '../components/Button'
import {
  dismissFinding,
  getChecks,
  groupBySeverity,
  restoreFinding,
  scoreTone,
  SEVERITIES,
  SEVERITY_LABELS,
  type Finding,
  type Report,
  type Severity
} from '../api/checks'
import { getMe } from '../api/auth'

// WIKI_URL documents every check and how the score is worked out.
const WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Goblin-Checks'

const ICONS: Record<Severity, typeof AlertOctagon> = {
  high: AlertOctagon,
  medium: AlertTriangle,
  low: Info,
  info: Info,
}

const ICON_TONE: Record<Severity, string> = {
  high: 'text-red-600 dark:text-red-400',
  medium: 'text-orange-600 dark:text-orange-400',
  low: 'text-blue-600 dark:text-blue-400',
  info: 'text-gray-500 dark:text-gray-400',
}

const BADGE_TONE: Record<Severity, string> = {
  high: 'bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-200',
  medium: 'bg-orange-100 text-orange-800 dark:bg-orange-900/50 dark:text-orange-200',
  low: 'bg-blue-100 text-blue-800 dark:bg-blue-900/50 dark:text-blue-200',
  info: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200',
}

const SCORE_TONE: Record<'success' | 'warning' | 'error', string> = {
  success: 'text-green-600 dark:text-green-400',
  warning: 'text-amber-600 dark:text-amber-400',
  error: 'text-red-600 dark:text-red-400',
}

export default function ChecksPage() {
  const queryClient = useQueryClient()
  const checks = useQuery({ queryKey: ['checks'], queryFn: getChecks, refetchInterval: 60_000 })
  // Read-only users get a 403 from the dismiss endpoints, so their buttons are
  // hidden instead of failing.
  const me = useQuery({ queryKey: ['me'], queryFn: getMe, retry: false })
  const canDismiss = me.data?.role === 'admin'
  const [dismissError, setDismissError] = useState<string | null>(null)

  const dismiss = useMutation({
    mutationFn: dismissFinding,
    onSuccess: () => {
      setDismissError(null)
      queryClient.invalidateQueries({ queryKey: ['checks'] })
    },
    onError: (e: Error) => setDismissError(e.message),
  })
  const restore = useMutation({
    mutationFn: restoreFinding,
    onSuccess: () => {
      setDismissError(null)
      queryClient.invalidateQueries({ queryKey: ['checks'] })
    },
    onError: (e: Error) => setDismissError(e.message),
  })

  if (checks.isLoading) return <p className="p-6 text-gray-500" data-testid="checks-loading">Running the checks…</p>
  if (checks.isError || !checks.data)
    return (
      <div className="p-6">
        <h1 className="mb-2 text-2xl font-bold text-gray-800 dark:text-white">Goblin Checks</h1>
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          The checks could not be run: {checks.error instanceof Error ? checks.error.message : 'unknown error'}
        </p>
      </div>
    )

  const report = checks.data
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">Goblin Checks</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400">
          A look at this box for the things that most often go wrong on a firewall.
        </p>
      </div>

      <ScoreCard report={report} />

      {dismissError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {dismissError}
        </p>
      )}

      <Findings
        report={report}
        canDismiss={canDismiss}
        busy={dismiss.isPending || restore.isPending}
        onDismiss={(key) => dismiss.mutate(key)}
        onRestore={(key) => restore.mutate(key)}
      />

      <details className="rounded-lg border border-gray-200 dark:border-gray-700" data-testid="passed-checks">
        <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-gray-700 dark:text-gray-200">
          Passed checks: {report.passed.length}
        </summary>
        <ul className="divide-y divide-gray-100 px-4 pb-2 dark:divide-gray-700">
          {report.passed.length === 0 && <li className="py-2 text-sm text-gray-500">Nothing passed yet.</li>}
          {report.passed.map((p) => (
            <li key={p.check} data-testid={`passed-${p.check}`} className="flex items-center gap-2 py-2 text-sm text-gray-700 dark:text-gray-200">
              <CheckCircle2 size={16} className="text-green-600 dark:text-green-400" />
              {p.title}
            </li>
          ))}
        </ul>
      </details>

      <p className="text-sm text-gray-500 dark:text-gray-400">
        <a
          href={WIKI_URL}
          target="_blank"
          rel="noreferrer"
          className="text-blue-600 hover:underline dark:text-blue-400"
        >
          How the score works
        </a>
      </p>
    </div>
  )
}

// ScoreCard is the big number at the top with a one-line reading of it.
function ScoreCard({ report }: { report: Report }) {
  const tone = SCORE_TONE[scoreTone(report.score)]
  const counts = SEVERITIES.map((s) => ({
    severity: s,
    n: report.findings.filter((f) => f.severity === s).length,
  })).filter((c) => c.n > 0)
  return (
    <section className="flex flex-wrap items-center gap-4 rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <div className={`text-5xl font-bold ${tone}`} data-testid="checks-score">
        {report.score}
      </div>
      <div className="min-w-[12rem] flex-1">
        <p className="text-sm font-medium text-gray-800 dark:text-gray-100">
          {report.findings.length === 0
            ? 'Nothing to fix — every check passed.'
            : `${report.findings.length} finding${report.findings.length === 1 ? '' : 's'} to look at`}
        </p>
        <p className="mt-1 flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
          {counts.length === 0 ? (
            <span>Score out of 100.</span>
          ) : (
            counts.map((c) => (
              <span key={c.severity} className={`rounded-full px-2 py-0.5 font-medium ${BADGE_TONE[c.severity]}`}>
                {c.n} {SEVERITY_LABELS[c.severity].toLowerCase()}
              </span>
            ))
          )}
          {report.dismissed.length > 0 && (
            <span data-testid="dismissed-count">{report.dismissed.length} dismissed and not counted</span>
          )}
        </p>
      </div>
    </section>
  )
}

interface FindingsProps {
  report: Report
  canDismiss: boolean
  busy: boolean
  onDismiss: (key: string) => void
  onRestore: (key: string) => void
}

function Findings({ report, canDismiss, busy, onDismiss, onRestore }: FindingsProps) {
  const groups = groupBySeverity(report.findings)
  return (
    <div className="space-y-5">
      {groups.length === 0 && (
        <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 p-4 text-sm text-green-800 dark:border-green-900 dark:bg-green-900/20 dark:text-green-200">
          <ShieldCheck size={18} />
          Nothing to fix. Everything the checks look at is in good shape.
        </div>
      )}
      {groups.map((g) => (
        <section key={g.severity} data-testid={`group-${g.severity}`}>
          <h2 className="mb-2 flex items-center gap-2 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {SEVERITY_LABELS[g.severity]} ({g.findings.length})
          </h2>
          <ul className="space-y-3">
            {g.findings.map((f) => (
              <FindingRow key={f.key} finding={f} canDismiss={canDismiss} busy={busy} onDismiss={onDismiss} />
            ))}
          </ul>
        </section>
      ))}

      <details className="rounded-lg border border-gray-200 dark:border-gray-700" data-testid="dismissed-checks">
        <summary className="cursor-pointer px-4 py-3 text-sm font-medium text-gray-700 dark:text-gray-200">
          {report.dismissed.length} dismissed
        </summary>
        <ul className="divide-y divide-gray-100 px-4 pb-2 dark:divide-gray-700">
          {report.dismissed.length === 0 && <li className="py-2 text-sm text-gray-500">Nothing dismissed.</li>}
          {report.dismissed.map((f) => (
            <li key={f.key} data-testid={`dismissed-${f.key}`} className="flex flex-wrap items-center gap-2 py-2 text-sm text-gray-600 dark:text-gray-300">
              <span className="flex-1">{f.title}</span>
              {canDismiss && (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => onRestore(f.key)}>
                  Restore
                </Button>
              )}
            </li>
          ))}
        </ul>
      </details>
    </div>
  )
}

function FindingRow({
  finding,
  canDismiss,
  busy,
  onDismiss
}: {
  finding: Finding
  canDismiss: boolean
  busy: boolean
  onDismiss: (key: string) => void
}) {
  const Icon = ICONS[finding.severity] ?? Info
  const [open, setOpen] = useState(false)
  const paths = finding.paths ?? []
  return (
    <li
      data-testid={`finding-${finding.key}`}
      className="rounded-lg border border-gray-200 p-3 dark:border-gray-700"
    >
      <div className="flex flex-wrap items-start gap-3">
        <Icon size={20} className={`mt-0.5 flex-shrink-0 ${ICON_TONE[finding.severity]}`} />
        <div className="min-w-[14rem] flex-1">
          <p className="font-medium text-gray-800 dark:text-gray-100">{finding.title}</p>
          <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">{finding.detail}</p>
          {paths.length > 0 && (
            <button
              type="button"
              onClick={() => setOpen((o) => !o)}
              className="mt-1 flex items-center gap-1 text-xs text-gray-500 hover:underline dark:text-gray-400"
            >
              {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
              Where in the config ({paths.length})
            </button>
          )}
          {open && (
            <ul className="mt-1 space-y-0.5 text-xs text-gray-500 dark:text-gray-400">
              {paths.map((p) => (
                <li key={p} className="font-mono">
                  {p}
                </li>
              ))}
            </ul>
          )}
        </div>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${BADGE_TONE[finding.severity]}`}>
          {SEVERITY_LABELS[finding.severity]}
        </span>
        <div className="flex items-center gap-2">
          <Link
            to={finding.link}
            className="rounded-md bg-blue-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-blue-700"
          >
            Fix it
          </Link>
          {canDismiss && (
            <Button size="sm" variant="outline" disabled={busy} onClick={() => onDismiss(finding.key)}>
              Dismiss
            </Button>
          )}
        </div>
      </div>
    </li>
  )
}
