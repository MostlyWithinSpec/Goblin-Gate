import React, { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { t } from '../../../i18n'
import { setSentryMode, type SentryStatus } from '../../../api/sentry'

// Each mode keeps the catalog key of its label and description: a constant whose
// value is a t() call at module load would freeze English. The keys are literal so
// the catalog check can find them, one switch per pair.
function modeText(mode: string): { label: string; desc: string } {
  switch (mode) {
    case 'off':
      return { label: t('sentry.modeOff'), desc: t('sentry.modeOffDesc') }
    case 'shadow':
      return { label: t('sentry.modeShadow'), desc: t('sentry.modeShadowDesc') }
    case 'auto':
      return { label: t('sentry.modeAuto'), desc: t('sentry.modeAutoDesc') }
    default:
      return { label: t('sentry.modeApprove'), desc: t('sentry.modeApproveDesc') }
  }
}

const MODES = ['off', 'shadow', 'approve', 'auto'] as const

export default function ModeSelector({ status }: { status?: SentryStatus }) {
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const current = status?.mode ?? 'off'

  const mut = useMutation({
    mutationFn: (mode: string) => setSentryMode(mode),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sentry'] })
      qc.invalidateQueries({ queryKey: ['plugins'] })
    }
  })

  const select = (mode: string) => {
    if (mode === current) return
    setBusy(true)
    mut.mutate(mode, { onSettled: () => setBusy(false) })
  }

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-gray-800 dark:text-white">{t('sentry.modeTitle')}</h3>
        {mut.isError && (
          <span role="alert" className="text-xs text-red-600">{(mut.error as Error).message}</span>
        )}
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {MODES.map((m) => {
          const text = modeText(m)
          return (
          <button
            key={m}
            type="button"
            onClick={() => select(m)}
            disabled={busy}
            className={`rounded-lg border p-3 text-left transition ${
              current === m
                ? 'border-blue-400 bg-blue-50 dark:border-blue-600 dark:bg-blue-900/30'
                : 'border-gray-200 hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600'
            }`}
          >
            <div className="mb-1 flex items-center gap-2">
              <span
                className={`h-2.5 w-2.5 rounded-full ${
                  current === m ? 'bg-blue-500' : 'bg-gray-300 dark:bg-gray-600'
                }`}
              />
              <span className="text-sm font-medium text-gray-800 dark:text-gray-200">{text.label}</span>
            </div>
            <p className="text-xs text-gray-500 dark:text-gray-400">{text.desc}</p>
          </button>
          )
        })}
      </div>
    </div>
  )
}
