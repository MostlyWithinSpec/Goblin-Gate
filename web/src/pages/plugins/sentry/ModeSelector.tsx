import React, { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { setSentryMode, type SentryStatus } from '../../../api/sentry'

const MODES = [
  { value: 'off', label: 'Off', desc: 'Goblin Sentry is disabled. No analysis or action.' },
  { value: 'shadow', label: 'Shadow', desc: 'Analyzes traffic and records decisions but takes no action.' },
  { value: 'approve', label: 'Approve', desc: 'Analyzes traffic, creates incidents, and waits for your approval before acting.' },
  { value: 'auto', label: 'Auto', desc: 'Applies matching rules automatically. Unmatched incidents still need approval.' }
] as const

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
        <h3 className="text-sm font-semibold text-gray-800 dark:text-white">Sentry mode</h3>
        {mut.isError && (
          <span role="alert" className="text-xs text-red-600">{(mut.error as Error).message}</span>
        )}
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-1 sm:grid-cols-4">
        {MODES.map((m) => (
          <button
            key={m.value}
            type="button"
            onClick={() => select(m.value)}
            disabled={busy}
            className={`rounded-lg border p-3 text-left transition ${
              current === m.value
                ? 'border-blue-400 bg-blue-50 dark:border-blue-600 dark:bg-blue-900/30'
                : 'border-gray-200 hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600'
            }`}
          >
            <div className="mb-1 flex items-center gap-2">
              <span
                className={`h-2.5 w-2.5 rounded-full ${
                  current === m.value ? 'bg-blue-500' : 'bg-gray-300 dark:bg-gray-600'
                }`}
              />
              <span className="text-sm font-medium text-gray-800 dark:text-gray-200">{m.label}</span>
            </div>
            <p className="text-xs text-gray-500 dark:text-gray-400">{m.desc}</p>
          </button>
        ))}
      </div>
    </div>
  )
}
