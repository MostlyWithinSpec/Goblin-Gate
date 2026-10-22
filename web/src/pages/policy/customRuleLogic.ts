// Client-side checks for custom nftables rules (task 101). Mirrors
// internal/config/customrules.go (CheckCustomRuleText and the checks around
// it); the server and `nft -c` stay authoritative — this only refuses the same
// cases before a PATCH so the message appears while typing.
import type { CustomRule } from '../../api/objects'

export const MAX_CUSTOM_RULE_LEN = 512

// The words a rule body may not contain outside double quotes: they would
// leave the rule (new tables, chains, flushes, includes) or jump into chains
// the generated ruleset owns.
export const CUSTOM_RULE_WORDS = [
  'table',
  'chain',
  'flush',
  'delete',
  'destroy',
  'insert',
  'replace',
  'create',
  'include',
  'define',
  'redefine',
  'undefine',
  'rename',
  'jump',
  'goto',
]

export const CUSTOM_HOOKS: { value: CustomRule['hook']; label: string }[] = [
  { value: 'input', label: 'input (traffic to the firewall)' },
  { value: 'forward', label: 'forward (traffic through the firewall)' },
  { value: 'prerouting', label: 'prerouting (NAT, before routing)' },
  { value: 'postrouting', label: 'postrouting (NAT, after routing)' },
]

// ruleTokens splits s into words outside double quotes; unquoted is s with the
// quoted parts replaced by a space, ok false for an unterminated quote.
function ruleTokens(s: string): { words: string[]; unquoted: string; ok: boolean } {
  let unquoted = ''
  let inQuote = false
  for (const r of s) {
    if (r === '"') {
      inQuote = !inQuote
      unquoted += ' '
    } else if (inQuote) {
      // quoted text is not inspected
    } else {
      unquoted += r
    }
  }
  const words = unquoted
    .split(/[\s{},]+/)
    .map((w) => w.trim())
    .filter(Boolean)
  return { words, unquoted, ok: !inQuote }
}

// checkCustomRuleText returns why a rule body is refused, or "" when it is
// shaped like a single rule. It does not check nftables syntax.
export function checkCustomRuleText(raw: string): string {
  const s = raw.trim()
  if (!s) return 'The rule is empty.'
  if (s.length > MAX_CUSTOM_RULE_LEN) return `The rule is longer than ${MAX_CUSTOM_RULE_LEN} characters.`
  if (/[\n\r]/.test(s)) return 'One rule per entry: no line breaks.'
  const { words, unquoted, ok } = ruleTokens(s)
  if (!ok) return 'Unbalanced quotes.'
  if (unquoted.includes("'")) return 'Use double quotes ("...") for strings: nft has no single-quoted strings.'
  if (/[;#]/.test(unquoted)) return "One rule per entry: no ';' or '#' outside quotes."
  let depth = 0
  for (const r of unquoted) {
    if (r === '{') depth++
    else if (r === '}') depth--
    if (depth < 0) break
  }
  if (depth !== 0) return 'Unbalanced braces.'
  for (const w of words) {
    const bad = CUSTOM_RULE_WORDS.find((x) => x.toLowerCase() === w.toLowerCase())
    if (bad) return `"${bad}" is not allowed: a custom rule is a single rule in a generated chain.`
  }
  return ''
}

// validateCustomRule mirrors what the server refuses for one entry, except the
// name (ConfigList checks the name pattern and duplicates).
export function validateCustomRule(item: CustomRule): Record<string, string> {
  const e: Record<string, string> = {}
  if (!CUSTOM_HOOKS.some((h) => h.value === item.hook)) e.hook = 'Choose a hook.'
  if (item.position && item.position !== 'start' && item.position !== 'end') e.position = 'Choose Start or End.'
  const bad = checkCustomRuleText(item.rule)
  if (bad) e.rule = bad
  if ((item.comment ?? '').length > 200 || /["\n\r]/.test(item.comment ?? ''))
    e.comment = 'Comment: at most 200 characters, no quotes or line breaks.'
  return e
}
