import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { BrainCircuit, Check, RotateCcw, Trash2, Play, AlertTriangle } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import SentryNav from './SentryNav'
import {
  getModels,
  trainModel,
  acceptModel,
  rollbackModel,
  putModelPolicy,
  getExamples,
  deleteExample,
  isPluginNotRunning,
  type ModelVersion,
  type ModelPolicy,
  type LearnedExample
} from '../../../api/sentry'

const fmtTime = (rfc3339: string) => new Date(rfc3339).toLocaleString()
const pct = (v: number) => `${Math.round(v * 100)}%`

// Tools that have a confidence threshold. Blocks never act on the model's own
// judgement, so there is no threshold for them.
const THRESHOLD_TOOLS = [
  { key: 'mark_false_positive', label: 'Mark false positive' },
  { key: 'add_watch', label: 'Add to watch list' },
  { key: 'notify_admin', label: 'Notify admin' },
  { key: 'query_reputation', label: 'Query reputation' }
]

const inputClass =
  'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'

export default function Learning() {
  const qc = useQueryClient()
  const [showReport, setShowReport] = useState<number | null>(null)
  const [policyDraft, setPolicyDraft] = useState<ModelPolicy | null>(null)
  const [trainError, setTrainError] = useState<string | null>(null)

  // Poll while a training job is running. A plain interval (not a callback)
  // matches the project convention and avoids this react-query version's
  // callback typing/runtime mismatch; polling every 2s always is harmless.
  const modelsQ = useQuery({
    queryKey: ['sentry', 'models'],
    queryFn: getModels,
    retry: false,
    refetchInterval: 2000
  })

  const examplesQ = useQuery({
    queryKey: ['sentry', 'model-examples'],
    queryFn: getExamples,
    retry: false
  })

  const invalidate = () => qc.invalidateQueries({ queryKey: ['sentry'] })

  const trainMut = useMutation({
    mutationFn: (kind: 'knn' | 'llm') => trainModel({ kind }),
    onSuccess: () => {
      setTrainError(null)
      invalidate()
    },
    onError: (e) => setTrainError((e as Error).message)
  })

  const acceptMut = useMutation({
    mutationFn: (version: number) => acceptModel(version),
    onSuccess: () => {
      setShowReport(null)
      invalidate()
    }
  })

  const rollbackMut = useMutation({
    mutationFn: () => rollbackModel(),
    onSuccess: invalidate,
    onError: (e) => setTrainError((e as Error).message)
  })

  const policyMut = useMutation({
    mutationFn: (policy: ModelPolicy) => putModelPolicy(policy),
    onSuccess: () => {
      setPolicyDraft(null)
      invalidate()
    },
    onError: (e) => setTrainError((e as Error).message)
  })

  const deleteMut = useMutation({
    mutationFn: (alertId: number) => deleteExample(alertId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sentry', 'model-examples'] })
      invalidate()
    },
    onError: (e) => setTrainError((e as Error).message)
  })

  if (modelsQ.error && isPluginNotRunning(modelsQ.error)) {
    return (
      <div>
        <SentryNav />
        <p className="mt-4 text-sm text-gray-500">Goblin Sentry is not running.</p>
      </div>
    )
  }

  const state = modelsQ.data
  const versions = state?.versions ?? []
  const policy = state?.policy
  const job = state?.job
  const currentVersion = versions.find((v) => v.version === state?.current)
  const examples = examplesQ.data?.examples ?? []
  const falsePositives = examples.filter((e) => e.answer === 'ignore')
  const busy = trainMut.isPending || acceptMut.isPending || rollbackMut.isPending || policyMut.isPending

  return (
    <div>
      <SentryNav />

      <div className="mt-4 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <BrainCircuit size={18} className="text-blue-600 dark:text-blue-400" />
          <h2 className="text-lg font-semibold text-gray-800 dark:text-white">G.O.B.L.I.N. learning</h2>
        </div>
        <div className="flex items-center gap-2">
          {state && state.current > 0 && (
            <>
              <span className="text-sm text-gray-500">
                Current: <strong>v{state.current}</strong>
              </span>
              <button
                type="button"
                onClick={() => rollbackMut.mutate()}
                disabled={busy || !state.previous}
                title={state.previous ? `Roll back to v${state.previous}` : 'No previous version to roll back to'}
                className="flex items-center gap-1 rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <RotateCcw size={14} /> Roll back
              </button>
            </>
          )}
          <button
            type="button"
            onClick={() => trainMut.mutate('knn')}
            disabled={busy || (!!job && !job.finished) || examples.length < 40}
            title={examples.length < 40 ? `Needs at least 40 answered incidents (have ${examples.length})` : 'Train a new model'}
            className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-40"
          >
            <Play size={14} /> Train
          </button>
        </div>
      </div>

      <p className="mt-2 max-w-3xl text-xs text-gray-500">
        The model is only asked when your rules would put an incident in front of you. It never blocks on its own —
        a block always becomes a suggestion you confirm. Read the evaluation report before accepting a new version.
      </p>

      {trainError && (
        <div role="alert" className="mt-3 flex items-start gap-2 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span>{trainError}</span>
        </div>
      )}

      {job && !job.finished && (
        <div className="mt-3 rounded bg-blue-50 p-2 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
          Training a {job.kind} model… this runs in the background. The page refreshes automatically when it is done.
        </div>
      )}

      {job?.finished && job.error && (
        <div role="alert" className="mt-3 flex items-start gap-2 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span>Training failed: {job.error}</span>
        </div>
      )}

      {/* Model versions + evaluation reports */}
      <h3 className="mt-6 text-base font-semibold text-gray-900 dark:text-white">Model versions</h3>
      {modelsQ.isLoading && <p className="mt-2 text-sm text-gray-500">Loading…</p>}
      {versions.length === 0 && !modelsQ.isLoading && (
        <p className="mt-2 text-sm text-gray-500">
          No model yet. Answer at least 40 incidents, then press Train.
        </p>
      )}

      <div className="mt-3 space-y-3">
        {versions.map((v) => (
          <VersionCard
            key={v.version}
            v={v}
            isCurrent={state?.current === v.version}
            showReport={showReport === v.version}
            onToggleReport={() => setShowReport(showReport === v.version ? null : v.version)}
            onAccept={() => acceptMut.mutate(v.version)}
            busy={busy}
          />
        ))}
      </div>

      {/* Confidence thresholds */}
      {policy && (
        <section className="mt-8">
          <h3 className="text-base font-semibold text-gray-900 dark:text-white">Confidence thresholds</h3>
          <p className="mt-1 max-w-3xl text-xs text-gray-500">
            The model only acts on its own when it is at least this confident, and the policy is set to “act”. Blocks
            have no threshold — they are always a suggestion. Use: <strong>{policy.use}</strong>
          </p>

          {policyDraft ? (
            <div className="mt-3 rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
              <label className="block text-sm">Policy use
                <select
                  value={policyDraft.use}
                  onChange={(e) => setPolicyDraft({ ...policyDraft, use: e.target.value as ModelPolicy['use'] })}
                  className={inputClass}
                >
                  <option value="off">off — model is not asked</option>
                  <option value="suggest">suggest — show advice in the queue</option>
                  <option value="act">act — act when confident enough</option>
                </select>
              </label>

              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                {THRESHOLD_TOOLS.map((t) => (
                  <label key={t.key} className="block text-sm">
                    {t.label} ({pct(policyDraft.thresholds[t.key] ?? 0.8)})
                    <input
                      type="range"
                      min={0.5}
                      max={1}
                      step={0.05}
                      value={policyDraft.thresholds[t.key] ?? 0.8}
                      onChange={(e) =>
                        setPolicyDraft({
                          ...policyDraft,
                          thresholds: { ...policyDraft.thresholds, [t.key]: Number(e.target.value) }
                        })
                      }
                    />
                  </label>
                ))}
              </div>

              <div className="mt-4 flex justify-end gap-2 border-t border-gray-200 pt-3 dark:border-gray-700">
                <button type="button" onClick={() => setPolicyDraft(null)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">
                  Cancel
                </button>
                <button
                  type="button"
                  disabled={policyMut.isPending}
                  onClick={() => policyMut.mutate(policyDraft)}
                  className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
                >
                  {policyMut.isPending ? 'Saving…' : 'Save thresholds'}
                </button>
              </div>
            </div>
          ) : (
            <div className="mt-3 flex items-center gap-4">
              {THRESHOLD_TOOLS.map((t) => (
                <span key={t.key} className="text-sm text-gray-600 dark:text-gray-300">
                  {t.label}: <strong>{pct(policy.thresholds[t.key] ?? 0.8)}</strong>
                </span>
              ))}
              <button
                type="button"
                onClick={() => setPolicyDraft({ use: policy.use, thresholds: { ...policy.thresholds } })}
                className="rounded border border-blue-300 px-3 py-1 text-sm text-blue-700 hover:bg-blue-50 dark:border-blue-700 dark:text-blue-300 dark:hover:bg-blue-900/30"
              >
                Edit
              </button>
            </div>
          )}
        </section>
      )}

      {/* Learned behaviour */}
      <section className="mt-8">
        <h3 className="text-base font-semibold text-gray-900 dark:text-white">
          Learned behaviour <span className="text-sm font-normal text-gray-500">({examples.length})</span>
        </h3>
        <p className="mt-1 max-w-3xl text-xs text-gray-500">
          The admin decisions the model learned from. Remove one if it was a mistake — it will be dropped from future
          training.
        </p>

        {examplesQ.isLoading && <p className="mt-2 text-sm text-gray-500">Loading…</p>}
        {examples.length === 0 && !examplesQ.isLoading && (
          <p className="mt-2 text-sm text-gray-500">No learned decisions yet.</p>
        )}

        <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
            <thead className="bg-gray-50 dark:bg-gray-800">
              <tr>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">When</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Signature</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Source</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">You answered</th>
                <th className="w-16 px-2 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-700/50 dark:bg-gray-800">
              {examples.map((ex) => (
                <ExampleRow key={ex.alert.id} ex={ex} onDelete={() => deleteMut.mutate(ex.alert.id)} />
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* False positives */}
      <section className="mt-8">
        <h3 className="text-base font-semibold text-gray-900 dark:text-white">
          False positives <span className="text-sm font-normal text-gray-500">({falsePositives.length})</span>
        </h3>
        <p className="mt-1 max-w-3xl text-xs text-gray-500">
          Incidents you told the model to ignore. These teach it what is not an attack — a wrong one here makes it
          miss real threats, so remove any that are mistakes.
        </p>

        {falsePositives.length === 0 && <p className="mt-2 text-sm text-gray-500">None recorded.</p>}

        <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
            <thead className="bg-gray-50 dark:bg-gray-800">
              <tr>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">When</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Signature</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">Source</th>
                <th className="w-16 px-2 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 bg-white dark:divide-gray-700/50 dark:bg-gray-800">
              {falsePositives.map((ex) => (
                <ExampleRow key={ex.alert.id} ex={ex} onDelete={() => deleteMut.mutate(ex.alert.id)} />
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  )
}

function ExampleRow({ ex, onDelete }: { ex: LearnedExample; onDelete: () => void }) {
  return (
    <tr className="hover:bg-gray-50 dark:hover:bg-gray-700/40">
      <td className="whitespace-nowrap px-3 py-2 text-xs text-gray-500">{fmtTime(ex.time)}</td>
      <td className="max-w-xs truncate px-3 py-2 text-sm" title={ex.alert.signature}>
        {ex.alert.signature}
      </td>
      <td className="whitespace-nowrap px-3 py-2 text-sm text-gray-600 dark:text-gray-300">
        {ex.enrichment.remote ?? ex.alert.src_ip}
      </td>
      <td className="px-3 py-2">
        <Badge variant={ex.answer === 'block' ? 'error' : ex.answer === 'ignore' ? 'warning' : 'success'}>
          {ex.answer}
        </Badge>
      </td>
      <td className="px-2 py-2 text-right">
        <button
          type="button"
          onClick={onDelete}
          className="rounded p-1 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30"
          title="Remove this learned decision"
        >
          <Trash2 size={14} />
        </button>
      </td>
    </tr>
  )
}

function VersionCard({
  v,
  isCurrent,
  showReport,
  onToggleReport,
  onAccept,
  busy
}: {
  v: ModelVersion
  isCurrent: boolean
  showReport: boolean
  onToggleReport: () => void
  onAccept: () => void
  busy: boolean
}) {
  const r = v.report
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-semibold text-gray-900 dark:text-white">v{v.version}</span>
        <Badge variant="secondary">{v.kind}</Badge>
        {isCurrent && <Badge variant="success">current</Badge>}
        <span className="text-xs text-gray-500">{fmtTime(v.created)}</span>
        <span className="text-xs text-gray-500">{v.examples} examples</span>

        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            onClick={onToggleReport}
            className="rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {showReport ? 'Hide report' : 'Evaluation report'}
          </button>
          {!isCurrent && (
            <button
              type="button"
              onClick={onAccept}
              disabled={busy}
              className="flex items-center gap-1 rounded bg-green-600 px-3 py-1 text-sm text-white hover:bg-green-700 disabled:opacity-50"
            >
              <Check size={14} /> Accept
            </button>
          )}
        </div>
      </div>

      {showReport && (
        <div className="mt-3 border-t border-gray-200 pt-3 dark:border-gray-700">
          <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-1 text-sm sm:grid-cols-4">
            <Stat label="Accuracy" value={pct(r.accuracy)} />
            <Stat label="Train / test" value={`${r.train} / ${r.test}`} />
            <Stat label="Blocked but ignored" value={String(r.blocked_ignored)} bad={r.blocked_ignored > 0} />
            <Stat label="Ignored but blocked" value={String(r.ignored_blocked)} bad={r.ignored_blocked > 0} />
          </dl>

          {/* Accuracy per action */}
          <div className="mt-3">
            <p className="mb-1 text-xs font-medium uppercase text-gray-500">Accuracy per action</p>
            <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-700">
              <thead>
                <tr>
                  <th className="px-2 py-1 text-left text-xs font-medium uppercase text-gray-500">Action</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">n</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Correct</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Precision</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Recall</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
                {Object.entries(r.per_label).map(([label, s]) => (
                  <tr key={label}>
                    <td className="px-2 py-1">{label}</td>
                    <td className="px-2 py-1 text-right text-gray-600 dark:text-gray-300">{s.n}</td>
                    <td className="px-2 py-1 text-right text-gray-600 dark:text-gray-300">
                      {s.correct}/{s.n}
                    </td>
                    <td className="px-2 py-1 text-right">{pct(s.precision)}</td>
                    <td className="px-2 py-1 text-right">{pct(s.recall)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Would-be wrong blocks */}
          {(r.blocked_ignored > 0 || r.ignored_blocked > 0) && (
            <div className="mt-3 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/20 dark:text-yellow-200">
              Would-be wrong blocks: the model blocked {r.blocked_ignored} incident(s) you would have ignored, and
              missed {r.ignored_blocked} you would have blocked. A high number means it is not safe to let this one act.
            </div>
          )}

          {/* Threshold analysis */}
          {r.thresholds.length > 0 && (
            <div className="mt-3">
              <p className="mb-1 text-xs font-medium uppercase text-gray-500">Acting above a confidence threshold</p>
              <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-700">
                <thead>
                  <tr>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Threshold</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Would act</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Wrong</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">Hidden attacks</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
                  {r.thresholds.map((t) => (
                    <tr key={t.threshold}>
                      <td className="px-2 py-1 text-right">{pct(t.threshold)}</td>
                      <td className="px-2 py-1 text-right text-gray-600 dark:text-gray-300">{t.acted}</td>
                      <td className={`px-2 py-1 text-right ${t.wrong > 0 ? 'text-red-600' : ''}`}>{t.wrong}</td>
                      <td className={`px-2 py-1 text-right ${t.hidden_attacks > 0 ? 'text-red-600' : ''}`}>
                        {t.hidden_attacks}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <p className="mt-3 text-xs text-gray-500">
            Novel incidents: {r.novel} ({r.novel_correct} right, {r.novel_acted} acted) · invalid predictions:{' '}
            {r.invalid} · latency {Math.round(r.latency_ms)} ms
          </p>
        </div>
      )}
    </div>
  )
}

function Stat({ label, value, bad }: { label: string; value: string; bad?: boolean }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase text-gray-500">{label}</dt>
      <dd className={`mt-0.5 ${bad ? 'text-red-600 dark:text-red-400' : 'text-gray-800 dark:text-gray-200'}`}>{value}</dd>
    </div>
  )
}
