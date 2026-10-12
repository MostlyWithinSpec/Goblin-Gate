import React, { useState } from 'react'
import type { Policy, PolicyObjects } from '../../api/policy'
import { EditDrawer } from '../../components/EditDrawer'
import { applySecurityLevel, validatePolicy, type FieldErrors, type SecurityLevel } from './policyLogic'

interface MultiSelectProps {
  label: string
  options: string[]
  value: string[]
  onChange: (v: string[]) => void
  error?: string
  // exclusive is the catch-all choice ("all"/"any"): picking it clears the
  // others, picking another clears it.
  exclusive?: string
}

function MultiSelect({ label, options, value, onChange, error, exclusive }: MultiSelectProps) {
  const toggle = (o: string) => {
    if (value.includes(o)) return onChange(value.filter((x) => x !== o))
    if (o === exclusive) return onChange([o])
    onChange([...value.filter((x) => x !== exclusive), o])
  }
  return (
    <fieldset className="mb-4">
      <legend className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{label}</legend>
      <div className="flex flex-wrap gap-1">
        {options.length === 0 && <span className="text-sm text-gray-500">none defined</span>}
        {options.map((o) => (
          <label
            key={o}
            className={`cursor-pointer rounded-full border px-2 py-0.5 text-xs ${
              value.includes(o)
                ? 'border-blue-600 bg-blue-600 text-white'
                : 'border-gray-300 text-gray-700 dark:border-gray-600 dark:text-gray-300'
            }`}
          >
            <input type="checkbox" className="sr-only" checked={value.includes(o)} onChange={() => toggle(o)} />
            {o}
          </label>
        ))}
      </div>
      {error && <p className="mt-1 text-xs text-red-600" role="alert">{error}</p>}
    </fieldset>
  )
}

interface ProfileSelectProps {
  label: string
  options: string[]
  value?: string
  onChange: (v: string | undefined) => void
}

function ProfileSelect({ label, options, value, onChange }: ProfileSelectProps) {
  return (
    <label className="block mb-3 text-sm text-gray-700 dark:text-gray-300">
      {label}
      <select
        aria-label={label}
        className="mt-1 block w-full rounded border-gray-300 dark:bg-gray-700 dark:border-gray-600"
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value || undefined)}
      >
        <option value="">— none —</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </label>
  )
}

interface PolicyEditorProps {
  policy: Policy
  isNew: boolean
  objects: PolicyObjects
  others: Policy[] // every other policy, for duplicate checks
  onSave: (p: Policy) => void
  onClose: () => void
}

export default function PolicyEditor({ policy, isNew, objects, others, onSave, onClose }: PolicyEditorProps) {
  const [p, setP] = useState<Policy>(policy)
  const [errors, setErrors] = useState<FieldErrors>({})
  const set = <K extends keyof Policy>(k: K, v: Policy[K]) => setP((old) => ({ ...old, [k]: v }))

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const errs = validatePolicy(p, objects, others)
    setErrors(errs)
    if (Object.keys(errs).length === 0) onSave(p)
  }

  const input = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 dark:bg-gray-700 dark:border-gray-600'
  return (
    <EditDrawer isOpen onClose={onClose} title={isNew ? 'New policy' : `Edit policy ${policy.id}`}>
      <form onSubmit={submit} noValidate>
        <div className="mb-4 grid grid-cols-3 gap-2">
          <label className="text-sm text-gray-700 dark:text-gray-300">
            ID
            <input
              aria-label="ID"
              type="number"
              min={1}
              className={input}
              value={p.id}
              onChange={(e) => set('id', Number(e.target.value))}
            />
            {errors.id && <p className="mt-1 text-xs text-red-600" role="alert">{errors.id}</p>}
          </label>
          <label className="col-span-2 text-sm text-gray-700 dark:text-gray-300">
            Name
            <input aria-label="Name" className={input} value={p.name} onChange={(e) => set('name', e.target.value)} />
            {errors.name && <p className="mt-1 text-xs text-red-600" role="alert">{errors.name}</p>}
          </label>
        </div>

        <MultiSelect label="Incoming interface" options={objects.interfaces} value={p.src_intf ?? []} onChange={(v) => set('src_intf', v)} error={errors.src_intf} />
        <MultiSelect label="Outgoing interface" options={objects.interfaces} value={p.dst_intf ?? []} onChange={(v) => set('dst_intf', v)} error={errors.dst_intf} />
        <MultiSelect label="Source" options={objects.addresses} value={p.src_addr ?? []} onChange={(v) => set('src_addr', v)} error={errors.src_addr} exclusive="all" />
        <MultiSelect label="Destination" options={objects.addresses} value={p.dst_addr ?? []} onChange={(v) => set('dst_addr', v)} error={errors.dst_addr} exclusive="all" />
        <MultiSelect label="Service" options={objects.services} value={p.service ?? []} onChange={(v) => set('service', v)} error={errors.service} exclusive="any" />

        <div className="mb-4 grid grid-cols-2 gap-2">
          <label className="text-sm text-gray-700 dark:text-gray-300">
            Action
            <select aria-label="Action" className={input} value={p.action} onChange={(e) => set('action', e.target.value as Policy['action'])}>
              <option value="accept">accept</option>
              <option value="deny">deny</option>
            </select>
          </label>
          <label className="text-sm text-gray-700 dark:text-gray-300">
            Log
            <select aria-label="Log" className={input} value={p.log ?? 'off'} onChange={(e) => set('log', e.target.value as Policy['log'])}>
              <option value="off">off</option>
              <option value="all">all traffic</option>
              <option value="deny">denied only</option>
            </select>
          </label>
        </div>
        <label className="mb-2 flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input type="checkbox" checked={!!p.nat} onChange={(e) => set('nat', e.target.checked)} />
          NAT (masquerade to the outgoing interface address)
        </label>
        <label className="mb-4 flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input type="checkbox" checked={p.enabled} onChange={(e) => set('enabled', e.target.checked)} />
          Enabled
        </label>

        <fieldset className="mb-4 rounded border border-gray-200 p-3 dark:border-gray-700">
          <legend className="px-1 text-sm font-semibold text-gray-800 dark:text-gray-200">Security Profiles</legend>
          <div className="mb-3 flex gap-1" role="group" aria-label="Security level">
            {(['off', 'basic', 'strict'] as SecurityLevel[]).map((l) => (
              <button
                key={l}
                type="button"
                className="rounded border border-gray-300 px-2 py-0.5 text-xs capitalize dark:border-gray-600 dark:text-gray-200"
                onClick={() => setP((old) => applySecurityLevel(old, l, objects))}
              >
                {l}
              </button>
            ))}
          </div>
          <ProfileSelect label="IPS" options={objects.profiles.ips} value={p.ips_profile} onChange={(v) => set('ips_profile', v)} />
          <ProfileSelect label="Web filter" options={objects.profiles.webfilter} value={p.webfilter_profile} onChange={(v) => set('webfilter_profile', v)} />
          <ProfileSelect label="DNS filter" options={objects.profiles.dnsfilter} value={p.dnsfilter_profile} onChange={(v) => set('dnsfilter_profile', v)} />
          <ProfileSelect label="Application control" options={objects.profiles.appctrl} value={p.appctrl_profile} onChange={(v) => set('appctrl_profile', v)} />
          <MultiSelect label="Threat feeds" options={objects.feeds} value={p.threat_feeds ?? []} onChange={(v) => set('threat_feeds', v)} />
        </fieldset>

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600 dark:text-gray-200">
            Cancel
          </button>
          <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700">
            {isNew ? 'Create' : 'Save'}
          </button>
        </div>
      </form>
    </EditDrawer>
  )
}
