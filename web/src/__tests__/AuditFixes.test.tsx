import { describe, expect, test } from 'vitest'
import { checkCustomRuleText } from '../pages/policy/customRuleLogic'
import { isHttpsURL } from '../pages/Tailscale'

describe('audit fixes', () => {
  test('custom rules refuse single-quoted strings like the server', () => {
    expect(checkCustomRuleText("tcp dport 80 accept comment 'x' include '/etc/passwd'")).toContain('double quotes')
    expect(checkCustomRuleText('log prefix "it\'s fine" accept')).toBe('')
  })

  test('only https login links are opened', () => {
    expect(isHttpsURL('https://login.tailscale.com/a/abc')).toBe(true)
    expect(isHttpsURL('https://headscale.example.net/register/x')).toBe(true)
    expect(isHttpsURL('javascript:alert(1)')).toBe(false)
    expect(isHttpsURL('http://login.tailscale.com/a/abc')).toBe(false)
    expect(isHttpsURL('not a url')).toBe(false)
  })
})
