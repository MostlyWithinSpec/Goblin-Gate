import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    patchSection: vi.fn(),
    getCustomRuleHits: vi.fn(),
    validateCandidate: vi.fn(),
  }
})

import * as api from '../api/objects'
import CustomRules from '../pages/policy/CustomRules'
import { checkCustomRuleText } from '../pages/policy/customRuleLogic'

const rule: api.CustomRule = {
  name: 'prom-lan',
  enabled: true,
  hook: 'input',
  position: 'start',
  rule: 'iifname "lan" tcp dport 9100 accept',
}

const cfg: Candidate = { custom_rules: [structuredClone(rule)] }

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getCustomRuleHits).mockResolvedValue([])
  vi.mocked(api.validateCandidate).mockResolvedValue({ valid: true, errors: [] })
})

describe('custom rule text validation (mirrors internal/config/customrules.go)', () => {
  const accepted = [
    'iifname "lan" tcp dport 9100 accept',
    'ip protocol gre counter name "gre" accept', // "gre" inside quotes is fine
    'iifname "eth0" ip saddr 203.0.113.81 tcp dport 443 accept',
    'iifname "eth0" meta l4proto { tcp, udp } th dport 53 accept', // braces are allowed
    'a'.repeat(512),
  ]
  const refused: [string, string][] = [
    ['', 'empty'],
    ['   ', 'empty'],
    ['a'.repeat(513), 'longer'],
    ['tcp dport 80 accept\nct state new drop', 'line breaks'],
    ['tcp dport 80 accept; drop', "';'"],
    ['tcp dport 80 accept # note', "'#'"],
    ['iifname "lan accept', 'quotes'],
    ['ip saddr { 10.0.0.0/8 accept', 'braces'],
    ['table inet x', 'table'],
    ['flush set blocklist', 'flush'],
    ['TCP DPORT 22 DELETE', 'delete'], // case-insensitive
    ['iifname "lan" jump my_chain', 'jump'],
    ['include "/etc/nftables.conf"', 'include'],
  ]

  it.each(accepted)('accepts %s', (input) => {
    expect(checkCustomRuleText(input)).toBe('')
  })

  it.each(refused)('refuses %s', (input, want) => {
    expect(checkCustomRuleText(input).toLowerCase()).toContain(want.toLowerCase())
  })
})

describe('CustomRules page', () => {
  it('shows the warning banner and the hits column', async () => {
    vi.mocked(api.getCustomRuleHits).mockResolvedValue([
      { name: 'prom-lan', packets: 1234, bytes: 74000 },
      { name: 'other', packets: 5, bytes: 300 },
    ])
    wrap(<CustomRules />)
    expect(await screen.findByText(/exactly as written/i)).toBeInTheDocument()
    expect(screen.getByText(/web UI/i)).toBeInTheDocument()
    await screen.findByText('1,234 packets')
    expect(screen.queryByText('5 packets')).toBeNull() // only rules in the list
  })

  it('adds a rule with the right section body', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({})
    wrap(<CustomRules />)
    const newBtn = await screen.findByRole('button', { name: /New rule/i })
    fireEvent.click(newBtn)

    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'log-telnet' } })
    fireEvent.change(screen.getByLabelText('Hook'), { target: { value: 'forward' } })
    fireEvent.change(screen.getByLabelText('Position'), { target: { value: 'start' } })
    fireEvent.change(screen.getByLabelText('Rule'), {
      target: { value: 'iifname "guest" tcp dport 23 log prefix "telnet " drop' },
    })
    fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'catch telnet attempts' } })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('custom_rules')
    expect(value).toEqual([
      {
        name: 'log-telnet',
        enabled: true,
        hook: 'forward',
        position: 'start',
        rule: 'iifname "guest" tcp dport 23 log prefix "telnet " drop',
        comment: 'catch telnet attempts',
      },
    ])
  })

  it('refuses a rule that uses a forbidden word before saving', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({})
    wrap(<CustomRules />)
    fireEvent.click(await screen.findByRole('button', { name: /New rule/i }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'oops' } })
    fireEvent.change(screen.getByLabelText('Rule'), { target: { value: 'flush chain inet goblin forward' } })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/"flush" is not allowed/)
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('Check sends the candidate and shows nft errors under the rule field', async () => {
    vi.mocked(api.validateCandidate).mockResolvedValue({
      valid: false,
      errors: [{ path: 'custom_rules[0].rule', message: 'custom rule "prom-lan": nft: syntax error, unexpected junk' }],
    })
    wrap(<CustomRules />)
    await screen.findByRole('button', { name: 'prom-lan' })
    fireEvent.click(screen.getByRole('button', { name: 'prom-lan' }))
    fireEvent.change(screen.getByLabelText('Rule'), { target: { value: 'tcp dport 9100 frobnicate' } })
    fireEvent.click(screen.getByRole('button', { name: /^Check$/i }))

    await waitFor(() => expect(api.patchSection).toHaveBeenCalledWith('custom_rules', [
      { name: 'prom-lan', enabled: true, hook: 'input', position: 'start', rule: 'tcp dport 9100 frobnicate' },
    ]))
    await waitFor(() => expect(api.validateCandidate).toHaveBeenCalled())
    const box = await screen.findByRole('status')
    expect(box).toHaveTextContent('nft: syntax error, unexpected junk')
  })

  it('Check reports a clean ruleset', async () => {
    wrap(<CustomRules />)
    await screen.findByRole('button', { name: 'prom-lan' })
    fireEvent.click(screen.getByRole('button', { name: 'prom-lan' }))
    fireEvent.click(screen.getByRole('button', { name: /^Check$/i }))

    const box = await screen.findByRole('status')
    expect(box).toHaveTextContent(/commit to apply it/i)
  })
})
