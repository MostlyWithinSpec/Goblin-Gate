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
import { t } from '../../i18n'
import { CUSTOM_HOOKS, validateCustomRule } from './customRuleLogic'

const hookLabel = (hook: string) =>
  hook === 'prerouting' || hook === 'postrouting' ? `${hook} ${t('customRules.natTag')}` : hook

const fmtBytes = (n: number) =>
  n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1024 / 1024).toFixed(1)} MB`

interface CheckState {
  running: boolean
  ok: boolean
  message: string
  // for is the rule the result is about; another rule (or an edited one)
  // does not show it
  for: string
}

const checkKey = (r: CustomRule) => `${r.name}\u0000${r.hook}\u0000${r.position ?? 'end'}\u0000${r.rule}`

export default function CustomRules() {
  const hits = useQuery({ queryKey: ['custom-rule-hits'], queryFn: getCustomRuleHits, refetchInterval: 30_000 })
  const [check, setCheck] = useState<CheckState | null>(null)

  const columns: Column<CustomRule>[] = [
    { label: t('customRules.col.hook'), render: (r) => hookLabel(r.hook) },
    { label: t('customRules.col.position'), render: (r) => (r.position === 'start' ? t('customRules.start') : t('customRules.end')) },
    {
      label: t('customRules.col.rule'),
      render: (r) => (
        <code className="block max-w-[26rem] truncate font-mono text-xs" title={r.rule}>
          {r.rule}
        </code>
      ),
    },
    {
      label: t('customRules.col.hits'),
      render: (r) => {
        if (!r.enabled) return <span className="text-gray-400">{t('customRules.disabled')}</span>
        const h = (hits.data ?? []).find((x) => x.name === r.name)
        if (!h) return <span className="text-gray-400">—</span>
        return (
          <span title={t('customRules.bytesMatched', { bytes: fmtBytes(h.bytes) })}>
            {t('customRules.packets', { count: h.packets, packets: h.packets.toLocaleString() })}
          </span>
        )
      },
    },
  ]

  // checkRule PATCHes the list with this entry and validates the whole
  // candidate; the server runs `nft -c` on the rendered ruleset when a custom
  // rule is enabled and blames the line on custom_rules[<i>].rule.
  const checkRule = async (item: CustomRule, ctx: FormCtx) => {
    const local = validateCustomRule(item)
    if (Object.keys(local).length) {
      setCheck({ running: false, ok: false, message: Object.values(local)[0], for: checkKey(item) })
      return
    }
    const saved = (ctx.items as CustomRule[]) ?? []
    const mine = ctx.index === null ? saved.length : ctx.index
    const next = [...saved.filter((_, i) => i !== mine), omitEmpty(item) as CustomRule]
    const key = checkKey(item)
    setCheck({ running: true, ok: false, message: t('customRules.checkingWithNft'), for: key })
    let patched = false
    try {
      await patchSection('custom_rules', next)
      patched = true
      const res = await validateCandidate()
      const path = `custom_rules[${mine}].rule`
      const here = (res.errors ?? []).filter((e) => e.path === path)
      const elsewhere = (res.errors ?? []).filter((e) => e.path !== path)
      if (here.length) setCheck({ running: false, ok: false, message: here[0].message, for: key })
      else if (res.valid) setCheck({ running: false, ok: true, message: t('customRules.nftAccepts'), for: key })
      else
        setCheck({
          running: false,
          ok: false,
          message: t('customRules.ruleFineOthers', { problems: elsewhere.map((e) => `${e.path}: ${e.message}`).join(' · ') }),
          for: key,
        })
    } catch (e) {
      setCheck({ running: false, ok: false, message: (e as Error).message, for: key })
    } finally {
      // Checking is not saving: put the saved list back, so a rule that is
      // checked and then cancelled does not stay in the candidate.
      if (patched) await patchSection('custom_rules', saved).catch(() => {})
    }
  }

  return (
    <ConfigList<CustomRule>
      title={t('customRules.title')}
      path="custom_rules"
      select={(cfg) => cfg.custom_rules}
      empty={() => ({ name: '', enabled: true, hook: 'forward', position: 'end', rule: '' })}
      columns={columns}
      wide
      banner={
        <div className="mb-3 flex gap-2 rounded border border-yellow-300 bg-yellow-50 p-3 text-sm text-yellow-900 dark:border-yellow-600 dark:bg-yellow-900/30 dark:text-yellow-100">
          <AlertTriangle size={18} className="mt-0.5 shrink-0" />
          <div>
            {t('customRules.banner1')} <strong>{t('customRules.bannerExactly')}</strong>{' '}
            {t('customRules.banner2')} <strong>{t('customRules.bannerAutoRollback')}</strong>{' '}
            {t('customRules.banner3')}
          </div>
        </div>
      }
      intro={
        <>
          {t('customRules.intro1')} <strong>{t('customRules.introStart')}</strong> {t('customRules.intro2')}{' '}
          <strong>{t('customRules.introEnd')}</strong> {t('customRules.intro3')}
        </>
      }
      validate={(item) => validateCustomRule(item)}
      clean={(r) => omitEmpty(r) as CustomRule}
      newLabel={t('customRules.newRule')}
      extraActions={(item, ctx) => (
        <div className="mr-auto flex min-w-0 flex-col items-start">
          <button
            type="button"
            disabled={check?.running}
            onClick={() => checkRule(item, ctx)}
            className="rounded border border-blue-300 px-3 py-1 text-blue-700 hover:bg-blue-50 disabled:opacity-50"
          >
            {check?.running ? t('customRules.checking') : t('customRules.check')}
          </button>
        </div>
      )}
      form={(item, set, ctx: FormCtx) => (
        <>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={item.enabled !== false} onChange={(e) => set({ enabled: e.target.checked })} /> {t('customRules.enabled')}
          </label>
          <label className="block">
            {t('customRules.hook')}
            <select
              aria-label={t('customRules.hook')}
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
            {t('customRules.position')}
            <select
              aria-label={t('customRules.position')}
              className={inputClass}
              value={item.position ?? 'end'}
              onChange={(e) => set({ position: e.target.value as CustomRule['position'] })}
            >
              <option value="start">{t('customRules.positionStart')}</option>
              <option value="end">{t('customRules.positionEnd')}</option>
            </select>
            {ctx.err('position')}
          </label>
          <label className="block">
            {t('customRules.rule')}
            <textarea
              aria-label={t('customRules.rule')}
              rows={3}
              spellCheck={false}
              placeholder={'iifname "lan" tcp dport 9100 accept'}
              className={inputClass + ' font-mono text-xs'}
              value={item.rule ?? ''}
              onChange={(e) => set({ rule: e.target.value })}
            />
            {ctx.err('rule')}
            <p className="mt-1 text-xs text-gray-500">
              {t('customRules.ruleHint1')} <code>;</code>, <code>#</code> {t('customRules.ruleHint2')}
            </p>
          </label>
          <label className="block">
            {t('customRules.comment')}
            <input aria-label={t('customRules.comment')} className={inputClass} value={item.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            {ctx.err('comment')}
          </label>
          {check && check.for === checkKey(item) && (
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
