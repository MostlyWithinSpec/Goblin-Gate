import React, { useEffect, useState } from 'react'
import { getDevices, getSetupState, submitSetup, type NetDevice, type SecurityLevel } from '../../api/setup'
import type { Errors } from '../network/networkLogic'
import {
  buildRequest,
  initialAnswers,
  passwordStrength,
  STEPS,
  stepForPath,
  suggestDHCPRange,
  validateStep,
  withCurrent,
  type Answers,
  type Step,
} from './setupLogic'

const LEVELS: { value: SecurityLevel; title: string; text: string }[] = [
  { value: 'off', title: 'Off', text: 'Just a firewall and router. Nothing is inspected or blocked beyond your policies.' },
  { value: 'basic', title: 'Basic', text: 'Blocks known attacks and dangerous websites by name. Good for most homes.' },
  { value: 'strict', title: 'Strict', text: 'Basic, plus web filtering and application control. May block some sites you want.' },
]

const input = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'

export default function SetupWizard() {
  const [step, setStep] = useState<Step>('Welcome')
  const [a, setA] = useState<Answers>(initialAnswers)
  const [errors, setErrors] = useState<Errors>({})
  const [devices, setDevices] = useState<NetDevice[]>([])
  const [devError, setDevError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ message: string; serverErrors: { path: string; message: string }[] } | null>(null)
  const [done, setDone] = useState(false)

  useEffect(() => {
    // start from what the box runs (ports/LAN set on the console), if known
    const state = Promise.resolve().then(getSetupState).catch(() => undefined)
    getDevices()
      .then(async (ds) => {
        setDevices(ds)
        const cur = (await state)?.current
        setA((x) => withCurrent(x, cur, ds.map((d) => d.name)))
      })
      .catch((e: Error) => setDevError(e.message))
  }, [])

  const set = <K extends keyof Answers>(k: K, v: Answers[K]) => setA((x) => ({ ...x, [k]: v }))
  const idx = STEPS.indexOf(step)

  const next = () => {
    const errs = validateStep(step, a)
    setErrors(errs)
    if (Object.keys(errs).length === 0) setStep(STEPS[idx + 1])
  }
  const back = () => {
    setErrors({})
    setStep(STEPS[idx - 1])
  }

  const apply = async () => {
    setBusy(true)
    setResult(null)
    try {
      const res = await submitSetup(buildRequest(a))
      if (res.ok) setDone(true)
      else setResult({ message: res.message, serverErrors: res.errors })
    } catch (e) {
      setResult({ message: (e as Error).message, serverErrors: [] })
    } finally {
      setBusy(false)
    }
  }

  const err = (k: string) => errors[k] && <p className="mt-1 text-xs text-red-600" role="alert">{errors[k]}</p>
  const deviceOptions = devices.map((d) => (
    <option key={d.name} value={d.name}>
      {d.name}
      {d.mac ? ` (${d.mac})` : ''}
      {d.up ? '' : ' - no link'}
    </option>
  ))
  const strength = passwordStrength(a.password)

  if (done) {
    const lanIP = a.lanAddress.split('/')[0]
    return (
      <Shell>
        <h2 className="text-xl font-semibold">All set</h2>
        <p className="mt-2">Goblin Gate is running with your settings.</p>
        <p className="mt-2">
          If your computer is on the LAN, reconnect at <code>https://{lanIP}</code> and sign in as <code>admin</code>.
        </p>
        <a href="/login" className="mt-4 inline-flex min-h-[40px] items-center rounded bg-blue-600 px-3 py-1 text-white">Go to sign in</a>
      </Shell>
    )
  }

  return (
    <Shell>
      <ol className="mb-6 flex flex-wrap gap-2 text-xs" aria-label="Setup steps">
        {STEPS.map((s, i) => (
          <li key={s} aria-current={s === step ? 'step' : undefined} className={`rounded px-2 py-1 ${i === idx ? 'bg-blue-600 text-white' : i < idx ? 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200' : 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400'}`}>
            {i + 1}. {s}
          </li>
        ))}
      </ol>

      <form
        noValidate
        aria-label={step}
        onSubmit={(ev: React.FormEvent) => {
          ev.preventDefault()
          if (step === 'Review') apply()
          else next()
        }}
        className="space-y-3 text-sm"
      >
        {step === 'Welcome' && (
          <>
            <h2 className="text-xl font-semibold">Welcome to Goblin Gate</h2>
            <p>This takes a few minutes. You will set an admin password, pick which network port goes to your modem (WAN) and which to your home network (LAN), and choose how much protection you want.</p>
            {devError && <p role="alert" className="text-red-600">Could not list network devices: {devError}</p>}
          </>
        )}

        {step === 'Admin password' && (
          <>
            <h2 className="text-xl font-semibold">Admin password</h2>
            <p>You sign in as <code>admin</code> with this password.</p>
            <label className="block">Password<input aria-label="Password" type="password" autoComplete="new-password" className={input} value={a.password} onChange={(e) => set('password', e.target.value)} />{err('password')}</label>
            <div aria-label="Password strength" className="flex items-center gap-2">
              <div className="h-2 flex-1 rounded bg-gray-200">
                <div className={`h-2 rounded ${['bg-red-500', 'bg-red-400', 'bg-yellow-400', 'bg-green-500', 'bg-green-600'][strength.score]}`} style={{ width: `${(strength.score / 4) * 100}%` }} />
              </div>
              <span className="text-xs">{strength.label}</span>
            </div>
            <label className="block">Confirm password<input aria-label="Confirm password" type="password" autoComplete="new-password" className={input} value={a.confirm} onChange={(e) => set('confirm', e.target.value)} />{err('confirm')}</label>
          </>
        )}

        {step === 'WAN' && (
          <>
            <h2 className="text-xl font-semibold">Internet (WAN)</h2>
            <label className="block">WAN device<select aria-label="WAN device" className={input} value={a.wanDevice} onChange={(e) => set('wanDevice', e.target.value)}><option value="">Choose…</option>{deviceOptions}</select>{err('wanDevice')}</label>
            <fieldset className="flex gap-4">
              <legend className="sr-only">WAN addressing</legend>
              <label className="flex items-center gap-1"><input type="radio" name="wanMode" checked={a.wanMode === 'dhcp'} onChange={() => set('wanMode', 'dhcp')} />Automatic (DHCP) - most ISPs</label>
              <label className="flex items-center gap-1"><input type="radio" name="wanMode" checked={a.wanMode === 'static'} onChange={() => set('wanMode', 'static')} />Static address</label>
            </fieldset>
            {a.wanMode === 'static' && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                <label>WAN address<input aria-label="WAN address" placeholder="203.0.113.2/24" className={input} value={a.wanAddress} onChange={(e) => set('wanAddress', e.target.value)} />{err('wanAddress')}</label>
                <label>Gateway<input aria-label="Gateway" placeholder="203.0.113.1" className={input} value={a.wanGateway} onChange={(e) => set('wanGateway', e.target.value)} />{err('wanGateway')}</label>
              </div>
            )}
          </>
        )}

        {step === 'LAN' && (
          <>
            <h2 className="text-xl font-semibold">Home network (LAN)</h2>
            <label className="block">LAN device<select aria-label="LAN device" className={input} value={a.lanDevice} onChange={(e) => set('lanDevice', e.target.value)}><option value="">Choose…</option>{deviceOptions}</select>{err('lanDevice')}</label>
            <label className="block">
              LAN address
              <input
                aria-label="LAN address"
                className={input}
                value={a.lanAddress}
                onChange={(e) => {
                  const v = e.target.value
                  const r = suggestDHCPRange(v)
                  setA((x) => ({ ...x, lanAddress: v, ...(r ? { dhcpStart: r.start, dhcpEnd: r.end } : {}) }))
                }}
              />
              {err('lanAddress')}
            </label>
            <label className="flex items-center gap-2"><input type="checkbox" checked={a.dhcp} onChange={(e) => set('dhcp', e.target.checked)} />Hand out addresses with DHCP</label>
            {a.dhcp && (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                <label>Range start<input aria-label="Range start" className={input} value={a.dhcpStart} onChange={(e) => set('dhcpStart', e.target.value)} />{err('dhcpStart')}</label>
                <label>Range end<input aria-label="Range end" className={input} value={a.dhcpEnd} onChange={(e) => set('dhcpEnd', e.target.value)} />{err('dhcpEnd')}</label>
              </div>
            )}
          </>
        )}

        {step === 'Security' && (
          <fieldset className="space-y-2">
            <legend className="mb-2 text-xl font-semibold">Security level</legend>
            {LEVELS.map((l) => (
              <label key={l.value} className={`block rounded border p-3 ${a.level === l.value ? 'border-blue-600 bg-blue-50 dark:bg-gray-700' : 'border-gray-300'}`}>
                <input type="radio" name="level" className="mr-2" checked={a.level === l.value} onChange={() => set('level', l.value)} />
                <strong>{l.title}</strong>
                <span className="block pl-5 text-gray-600 dark:text-gray-300">{l.text}</span>
              </label>
            ))}
            <p className="text-xs text-gray-500">You can change this later per policy.</p>
          </fieldset>
        )}

        {step === 'Review' && (
          <>
            <h2 className="text-xl font-semibold">Review</h2>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
              <dt>Admin</dt><dd>admin (password set)</dd>
              <dt>WAN</dt><dd>{a.wanDevice}, {a.wanMode === 'dhcp' ? 'DHCP' : `${a.wanAddress} via ${a.wanGateway}`}</dd>
              <dt>LAN</dt><dd>{a.lanDevice}, {a.lanAddress}</dd>
              <dt>DHCP</dt><dd>{a.dhcp ? `${a.dhcpStart} – ${a.dhcpEnd}` : 'off'}</dd>
              <dt>Security</dt><dd>{LEVELS.find((l) => l.value === a.level)!.title}</dd>
            </dl>
            {result && (
              <div role="alert" className="rounded bg-red-50 p-2 text-red-700">
                <p>{result.message}</p>
                <ul>
                  {result.serverErrors.map((v) => (
                    <li key={v.path + v.message}>
                      <button type="button" className="underline" onClick={() => setStep(stepForPath(v.path))}>{stepForPath(v.path)}</button>: {v.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}

        <div className="flex justify-between pt-4">
          <button type="button" onClick={back} disabled={idx === 0 || busy} className="rounded border border-gray-300 px-3 py-1 disabled:invisible">Back</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50">
            {step === 'Review' ? (busy ? 'Applying…' : 'Apply') : step === 'Welcome' ? 'Start' : 'Next'}
          </button>
        </div>
      </form>
    </Shell>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-gray-50 p-4 dark:bg-transparent">
      <div className="mb-6 flex items-center gap-3">
        <img src="/goblin-128.png" alt="" className="h-14 w-14 goblin-glow" />
        <span className="font-display text-xl font-extrabold tracking-wide text-gray-800 dark:text-gray-100">
          Goblin <span className="text-green-600 dark:text-green-400">Gate</span>
        </span>
      </div>
      <div className="w-full max-w-xl rounded-xl border border-gray-200 bg-white p-5 text-gray-800 shadow-lg dark:border-gray-700 dark:bg-gray-800/90 dark:text-gray-200 sm:p-8">{children}</div>
    </div>
  )
}
