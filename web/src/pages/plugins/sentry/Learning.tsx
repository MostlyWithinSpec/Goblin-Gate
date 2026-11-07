import React, { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { BrainCircuit, Check, RotateCcw, Trash2, Play, AlertTriangle } from 'lucide-react'
import { Badge } from '../../../components/Badge'
import { t } from '../../../i18n'
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

// Tools with a confidence threshold. Blocks never act on the model's own
// judgement, so there is no threshold for them.
const THRESHOLD_TOOLS = ['mark_false_positive', 'add_watch', 'notify_admin', 'query_reputation']

// toolLabel puts a tool through the catalog. A constant whose value is a t() call
// at module load would freeze English, so every tool gets its own literal key in
// here.
function toolLabel(tool: string): string {
  switch (tool) {
    case 'mark_false_positive':
      return t('learning.toolMarkFalsePositive')
    case 'add_watch':
      return t('learning.toolAddWatch')
    case 'notify_admin':
      return t('learning.toolNotifyAdmin')
    default:
      return t('learning.toolQueryReputation')
  }
}

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
        <p className="mt-4 text-sm text-gray-500">{t('learning.notRunning')}</p>
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
          <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('learning.title')}</h2>
        </div>
        <div className="flex items-center gap-2">
          {state && state.current > 0 && (
            <>
              <span className="text-sm text-gray-500">
                {t('learning.current')} <strong>v{state.current}</strong>
              </span>
              <button
                type="button"
                onClick={() => rollbackMut.mutate()}
                disabled={busy || !state.previous}
                title={state.previous ? t('learning.rollbackTo', { version: state.previous }) : t('learning.noPrevious')}
                className="flex items-center gap-1 rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <RotateCcw size={14} /> {t('learning.rollback')}
              </button>
            </>
          )}
          <button
            type="button"
            onClick={() => trainMut.mutate('knn')}
            disabled={busy || (!!job && !job.finished) || examples.length < 40}
            title={
              examples.length < 40
                ? t('learning.needsLead') + ' ' + t('learning.needsHave', { count: examples.length }) + ')'
                : t('learning.trainNewModel')
            }
            className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-40"
          >
            <Play size={14} /> {t('learning.train')}
          </button>
        </div>
      </div>

      <p className="mt-2 max-w-3xl text-xs text-gray-500">
        {t('learning.modelAsksLead')} {t('learning.modelAsksMid')} {t('learning.modelAsksRest')}
      </p>

      {trainError && (
        <div role="alert" className="mt-3 flex items-start gap-2 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span>{trainError}</span>
        </div>
      )}

      {job && !job.finished && (
        <div className="mt-3 rounded bg-blue-50 p-2 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
          {t('learning.training', { kind: job.kind })}
          {' '}
          {t('learning.trainingRest')}
        </div>
      )}

      {job?.finished && job.error && (
        <div role="alert" className="mt-3 flex items-start gap-2 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span>
            {t('learning.trainingFailed')} {job.error}
          </span>
        </div>
      )}

      {/* Model versions + evaluation reports */}
      <h3 className="mt-6 text-base font-semibold text-gray-900 dark:text-white">{t('learning.versions')}</h3>
      {modelsQ.isLoading && <p className="mt-2 text-sm text-gray-500">{t('common.loading')}</p>}
      {versions.length === 0 && !modelsQ.isLoading && (
        <p className="mt-2 text-sm text-gray-500">
          {t('learning.noModelLead')}
          {' '}
          {t('learning.noModelMid')}
          {' '}
          {t('learning.noModelRest')}
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
          <h3 className="text-base font-semibold text-gray-900 dark:text-white">{t('learning.thresholds')}</h3>
          <p className="mt-1 max-w-3xl text-xs text-gray-500">
            {t('learning.thresholdsLead')}
            {' '}
            {t('learning.thresholdsMid')}
            {' '}
            {t('learning.thresholdsRest')}
            {' '}
            {t('learning.thresholdsUse')} <strong>{policy.use}</strong>
          </p>

          {policyDraft ? (
            <div className="mt-3 rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
              <label className="block text-sm">
                {t('learning.policyUse')}
                <select
                  value={policyDraft.use}
                  onChange={(e) => setPolicyDraft({ ...policyDraft, use: e.target.value as ModelPolicy['use'] })}
                  className={inputClass}
                >
                  <option value="off">{t('learning.useOff')}</option>
                  <option value="suggest">{t('learning.useSuggest')}</option>
                  <option value="act">{t('learning.useAct')}</option>
                </select>
              </label>

              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                {THRESHOLD_TOOLS.map((key) => (
                  <label key={key} className="block text-sm">
                    {toolLabel(key)} ({pct(policyDraft.thresholds[key] ?? 0.8)})
                    <input
                      type="range"
                      min={0.5}
                      max={1}
                      step={0.05}
                      value={policyDraft.thresholds[key] ?? 0.8}
                      onChange={(e) =>
                        setPolicyDraft({
                          ...policyDraft,
                          thresholds: { ...policyDraft.thresholds, [key]: Number(e.target.value) }
                        })
                      }
                    />
                  </label>
                ))}
              </div>

              <div className="mt-4 flex justify-end gap-2 border-t border-gray-200 pt-3 dark:border-gray-700">
                <button type="button" onClick={() => setPolicyDraft(null)} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600">
                  {t('common.cancel')}
                </button>
                <button
                  type="button"
                  disabled={policyMut.isPending}
                  onClick={() => policyMut.mutate(policyDraft)}
                  className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
                >
                  {policyMut.isPending ? t('learning.saving') : t('learning.saveThresholds')}
                </button>
              </div>
            </div>
          ) : (
            <div className="mt-3 flex items-center gap-4">
              {THRESHOLD_TOOLS.map((key) => (
                <span key={key} className="text-sm text-gray-600 dark:text-gray-300">
                  {toolLabel(key)}: <strong>{pct(policy.thresholds[key] ?? 0.8)}</strong>
                </span>
              ))}
              <button
                type="button"
                onClick={() => setPolicyDraft({ use: policy.use, thresholds: { ...policy.thresholds } })}
                className="rounded border border-blue-300 px-3 py-1 text-sm text-blue-700 hover:bg-blue-50 dark:border-blue-700 dark:text-blue-300 dark:hover:bg-blue-900/30"
              >
                {t('learning.edit')}
              </button>
            </div>
          )}
        </section>
      )}

      {/* Learned behaviour */}
      <section className="mt-8">
        <h3 className="text-base font-semibold text-gray-900 dark:text-white">
          {t('learning.learned')} <span className="text-sm font-normal text-gray-500">({examples.length})</span>
        </h3>
        <p className="mt-1 max-w-3xl text-xs text-gray-500">
          {t('learning.learnedLead')}
          {' '}
          {t('learning.learnedRest')}
        </p>

        {examplesQ.isLoading && <p className="mt-2 text-sm text-gray-500">{t('common.loading')}</p>}
        {examples.length === 0 && !examplesQ.isLoading && (
          <p className="mt-2 text-sm text-gray-500">{t('learning.noLearned')}</p>
        )}

        <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
            <thead className="bg-gray-50 dark:bg-gray-800">
              <tr>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colWhen')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colSignature')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colSource')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colAnswered')}</th>
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
          {t('learning.falsePositives')} <span className="text-sm font-normal text-gray-500">({falsePositives.length})</span>
        </h3>
        <p className="mt-1 max-w-3xl text-xs text-gray-500">
          {t('learning.falseLead')}
          {' '}
          {t('learning.falseMid')}
          {' '}
          {t('learning.falseRest')}
        </p>

        {falsePositives.length === 0 && <p className="mt-2 text-sm text-gray-500">{t('learning.noneRecorded')}</p>}

        <div className="mt-3 overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
            <thead className="bg-gray-50 dark:bg-gray-800">
              <tr>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colWhen')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colSignature')}</th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colSource')}</th>
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
          title={t('learning.removeTitle')}
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
        <span className="text-xs text-gray-500">{t('learning.examplesCount', { count: v.examples })}</span>

        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            onClick={onToggleReport}
            className="rounded border border-gray-300 px-3 py-1 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {showReport ? t('learning.hideReport') : t('learning.evaluationReport')}
          </button>
          {!isCurrent && (
            <button
              type="button"
              onClick={onAccept}
              disabled={busy}
              className="flex items-center gap-1 rounded bg-green-600 px-3 py-1 text-sm text-white hover:bg-green-700 disabled:opacity-50"
            >
              <Check size={14} /> {t('learning.accept')}
            </button>
          )}
        </div>
      </div>

      {showReport && (
        <div className="mt-3 border-t border-gray-200 pt-3 dark:border-gray-700">
          <dl className="grid grid-cols-2 gap-x-8 gap-y-1 text-sm sm:grid-cols-4">
            <Stat label={t('learning.statAccuracy')} value={pct(r.accuracy)} />
            <Stat label={t('learning.statTrainTest')} value={`${r.train} / ${r.test}`} />
            <Stat label={t('learning.statBlockedIgnored')} value={String(r.blocked_ignored)} bad={r.blocked_ignored > 0} />
            <Stat label={t('learning.statIgnoredBlocked')} value={String(r.ignored_blocked)} bad={r.ignored_blocked > 0} />
          </dl>

          {/* Accuracy per action */}
          <div className="mt-3">
            <p className="mb-1 text-xs font-medium uppercase text-gray-500">{t('learning.accuracyPerAction')}</p>
            <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-700">
              <thead>
                <tr>
                  <th className="px-2 py-1 text-left text-xs font-medium uppercase text-gray-500">{t('learning.colAction')}</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colN')}</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colCorrect')}</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colPrecision')}</th>
                  <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colRecall')}</th>
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
              {t('learning.wrongBlocksLead')}
              {' '}
              {t('learning.wrongBlocksMid', { blocked: r.blocked_ignored })}
              {' '}
              {t('learning.wrongBlocksRest', { missed: r.ignored_blocked })}
              {' '}
              {t('learning.wrongBlocksEnd')}
            </div>
          )}

          {/* Threshold analysis */}
          {r.thresholds.length > 0 && (
            <div className="mt-3">
              <p className="mb-1 text-xs font-medium uppercase text-gray-500">{t('learning.actingAbove')}</p>
              <table className="min-w-full divide-y divide-gray-200 text-sm dark:divide-gray-700">
                <thead>
                  <tr>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colThreshold')}</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colWouldAct')}</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colWrong')}</th>
                    <th className="px-2 py-1 text-right text-xs font-medium uppercase text-gray-500">{t('learning.colHidden')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
                  {r.thresholds.map((row) => (
                    <tr key={row.threshold}>
                      <td className="px-2 py-1 text-right">{pct(row.threshold)}</td>
                      <td className="px-2 py-1 text-right text-gray-600 dark:text-gray-300">{row.acted}</td>
                      <td className={`px-2 py-1 text-right ${row.wrong > 0 ? 'text-red-600' : ''}`}>{row.wrong}</td>
                      <td className={`px-2 py-1 text-right ${row.hidden_attacks > 0 ? 'text-red-600' : ''}`}>
                        {row.hidden_attacks}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          <p className="mt-3 text-xs text-gray-500">
            {t('learning.novelLead', { novel: r.novel })}
            {' '}
            {t('learning.novelMid', { right: r.novel_correct, acted: r.novel_acted })}
            {' · '}
            {t('learning.novelInvalid')} {r.invalid} {' · '}
            {t('learning.latency')} {Math.round(r.latency_ms)} {' ms'}
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
