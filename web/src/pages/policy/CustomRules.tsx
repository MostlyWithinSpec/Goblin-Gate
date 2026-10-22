// Policy & Objects → Custom rules (task 101): admin-written nftables rules for
// what the policy model cannot express. Rules go into the ruleset as written,
// so the page warns about that, lets the admin check a rule against the server
// (`nft -c`) before committing, and shows each rule's counter.
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import type { CustomRule } from '../../api/objects'
import { getCustomRuleHits, patchSection, validateCandidate } from '../../api/objects'
import { ConfigList, inputClass, omitEmpty, type Column, type FormCtx } from '../../components/ConfigList'
import { CUSTOM_HOOKS, validateCustomRule } from './customRuleLogic'

const hookLabel = (hook: string) =>
  hook === 'prerouting' || hook === 'postrouting' ? `${hook} (nat)` : hook

const fmtBytes = (n: number) =>
  n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`

interface CheckState {
  running: boolean
  ok: boolean
  message: string
}

export default function CustomRules() {
  const hits = useQuery({ queryKey: ['custom-rule-hits'], queryFn: getCustomRuleHits, refetchInterval: 30_000 })
  const [check, setCheck] = useState<CheckState | null>(null)

  const columns: Column<CustomRule>[] = [
    { label: 'Hook', render: (r) => hookLabel(r.hook) },
    { label: 'Position', render: (r) => (r.position === 'start' ? 'Start' : 'End') },
    {
      label: 'Rule',
      render: (r) => (
        <code className="block max-w-[26rem] truncate font-mono text-xs" title={r.rule}>
          {r.rule}
        </code>
      ),
    },
    {
      label: 'Hits',
      render: (r) => {
        if (!r.enabled) return <span className="text-gray-400">disabled</span>
        const h = (hits.data ?? []).find((x) => x.name === r.name)
        if (!h) return <span className="text-gray-400">—</span>
        return <span title={`${fmtBytes(h.bytes)} matched`}>{h.packets.toLocaleString()} packets</span>
      },
    },
  ]

  // checkRule PATCHes the list with this entry and validates the whole
  // candidate; the server runs `nft -c` on the rendered ruleset when a custom
  // rule is enabled and blames the line on custom_rules[<i>].rule.
  const checkRule = async (item: CustomRule, ctx: FormCtx) => {
    const local = validateCustomRule(item)
    if (Object.keys(local).length) {
      setCheck({ running: false, ok: false, message: Object.values(local)[0] })
      return
    }
    const saved = (ctx.items as CustomRule[]) ?? []
    const mine = ctx.index === null ? saved.length : ctx.index
    const next = [...saved.filter((_, i) => i !== mine), omitEmpty(item) as CustomRule]
    setCheck({ running: true, ok: false, message: 'Checking with nft…' })
    try {
      await patchSection('custom_rules', next)
      const res = await validateCandidate()
      const path = `custom_rules[${mine}].rule`
      const here = (res.errors ?? []).filter((e) => e.path === path)
      const elsewhere = (res.errors ?? []).filter((e) => e.path !== path)
      if (here.length) setCheck({ running: false, ok: false, message: here[0].message })
      else if (res.valid) setCheck({ running: false, ok: true, message: 'nft accepts the ruleset. Commit to apply it.' })
      else
        setCheck({
          running: false,
          ok: false,
          message: `This rule is fine; the config has other problems: ${elsewhere
            .map((e) => `${e.path}: ${e.message}`)
            .join(' · ')}`,
        })
    } catch (e) {
      setCheck({ running: false, ok: false, message: (e as Error).message })
    }
  }

  return (
    <ConfigList<CustomRule>
      title="Custom nftables rules"
      path="custom_rules"
      select={(cfg) => cfg.custom_rules}
      empty={() => ({ name: '', enabled: true, hook: 'forward', position: 'end', rule: '' })}
      columns={columns}
      wide
      banner={
        <div className="mb-3 flex gap-2 rounded border border-yellow-300 bg-yellow-50 p-3 text-sm text-yellow-900 dark:border-yellow-600 dark:bg-yellow-900/30 dark:text-yellow-100">
          <AlertTriangle size={18} className="mt-0.5 shrink-0" />
          <div>
            These rules go into the firewall <strong>exactly as written</strong>: they bypass the policy
            model, its ordering and its safety rules. A wrong rule can block traffic you need — including
            this web UI. Commit with <strong>auto-rollback</strong> so a ruleset that locks you out is
            replaced by the previous one on its own, and keep a console open while you test.
          </div>
        </div>
      }
      intro={
        <>
          One nftables rule per entry. <strong>Start</strong> places it after the built-in safety rules and
          before everything the box generates; <strong>End</strong> places it after all of that, just before
          the chain policy. Use a firewall policy whenever one can express what you need — a custom rule is
          never checked against the policy model.
        </>
      }
      validate={(item) => validateCustomRule(item)}
      clean={(r) => omitEmpty(r) as CustomRule}
      newLabel="New rule"
      extraActions={(item, ctx) => (
        <div className="mr-auto flex min-w-0 flex-col items-start">
          <button
            type="button"
            disabled={check?.running}
            onClick={() => checkRule(item, ctx)}
            className="rounded border border-blue-300 px-3 py-1 text-blue-700 hover:bg-blue-50 disabled:opacity-50"
          >
            {check?.running ? 'Checking…' : 'Check'}
          </button>
        </div>
      )}
      form={(item, set, ctx: FormCtx) => (
        <>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={item.enabled !== false} onChange={(e) => set({ enabled: e.target.checked })} /> Enabled
          </label>
          <label className="block">
            Hook
            <select
              aria-label="Hook"
              className={inputClass}
              value={item.hook}
              onChange={(e) => set({ hook: e.target.value as CustomRule['hook'] })}
            >
              {CUSTOM_HOOKS.map((h) => (
                <option key={h.value} value={h.value}>
                  {h.label}
                </option>
              ))}
            </select>
            {ctx.err('hook')}
          </label>
          <label className="block">
            Position
            <select
              aria-label="Position"
              className={inputClass}
              value={item.position ?? 'end'}
              onChange={(e) => set({ position: e.target.value as CustomRule['position'] })}
            >
              <option value="start">Start — after the safety rules, before the generated rules</option>
              <option value="end">End — after everything, before the chain policy</option>
            </select>
            {ctx.err('position')}
          </label>
          <label className="block">
            Rule
            <textarea
              aria-label="Rule"
              rows={3}
              spellCheck={false}
              placeholder={'iifname "lan" tcp dport 9100 accept'}
              className={inputClass + ' font-mono text-xs'}
              value={item.rule ?? ''}
              onChange={(e) => set({ rule: e.target.value })}
            />
            {ctx.err('rule')}
            <p className="mt-1 text-xs text-gray-500">
              One rule: no <code>;</code>, <code>#</code> or line break outside quotes, and no building or
              deleting a table, chain or set.
            </p>
          </label>
          <label className="block">
            Comment
            <input aria-label="Comment" className={inputClass} value={item.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            {ctx.err('comment')}
          </label>
          {check && (
            <div
              role="status"
              className={
                'flex gap-2 rounded p-2 text-xs ' +
                (check.ok
                  ? 'bg-green-50 text-green-800 dark:bg-green-900/30 dark:text-green-100'
                  : 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-100')
              }
            >
              {check.ok && <CheckCircle2 size={16} className="mt-0.5 shrink-0" />}
              <span className="break-words font-mono">{check.message}</span>
            </div>
          )}
        </>
      )}
    />
  )
}
