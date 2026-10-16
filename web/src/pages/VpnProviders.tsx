// VPN providers (config "vpn.wireguard_clients"): WireGuard tunnels to a
// provider that act as gateways. The private key is stored outside the
// config; here you show the generated public key or paste the provider's key.
import { useEffect, useState } from 'react'
import { ConfigList, inputClass, ListInput, omitEmpty } from '../components/ConfigList'
import type { WireGuardClient } from '../api/objects'
import { getWireGuardClientPublicKey, setWireGuardClientPrivateKey } from '../api/gateways'
import { validateWireGuardClient } from './network/networkLogic'

function KeyModal({ name, mode, onClose }: { name: string; mode: 'show' | 'paste'; onClose: () => void }) {
  const [key, setKey] = useState('')
  const [result, setResult] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (mode === 'show') {
      setBusy(true)
      setError(null)
      getWireGuardClientPublicKey(name)
        .then((r) => setResult(r.public_key))
        .catch((e: Error) => setError(e.message))
        .finally(() => setBusy(false))
    }
  }, [mode, name])

  const paste = async () => {
    setBusy(true)
    setError(null)
    try {
      setResult((await setWireGuardClientPrivateKey(name, key.trim())).public_key)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50">
      <div className="w-full max-w-md rounded bg-white p-6 dark:bg-gray-800">
        <h3 className="mb-3 text-lg font-semibold text-gray-800 dark:text-gray-100">
          {mode === 'show' ? 'Public key' : 'Paste private key'} — {name}
        </h3>
        {busy && <p className="text-sm text-gray-500">Working…</p>}
        {mode === 'paste' && !result && (
          <label className="block text-sm text-gray-700 dark:text-gray-300">
            Private key
            <textarea
              aria-label="Private key"
              rows={3}
              className={inputClass + ' font-mono'}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="Paste the provider's private key"
            />
          </label>
        )}
        {result && <pre className="mt-2 break-all rounded bg-gray-100 p-3 font-mono text-xs dark:bg-gray-700">{result}</pre>}
        {error && <div role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
        <div className="mt-4 flex justify-end gap-2">
          {mode === 'paste' && !result && (
            <button type="button" disabled={busy || !key.trim()} onClick={paste} className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
              Save key
            </button>
          )}
          <button type="button" onClick={onClose} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600 dark:text-gray-200">Close</button>
        </div>
      </div>
    </div>
  )
}

export default function VpnProviders() {
  const [keyModal, setKeyModal] = useState<{ name: string; mode: 'show' | 'paste' } | null>(null)

  return (
    <ConfigList<WireGuardClient>
      title="VPN Providers"
      path="vpn.wireguard_clients"
      newLabel="New provider"
      intro="WireGuard tunnels to VPN providers (Mullvad, Proton, …) that act as gateways. The private key is never stored in the config."
      select={(c) => c.vpn?.wireguard_clients}
      empty={() => ({ name: '', enabled: true, address: '', peer_public_key: '', endpoint: '', kill_switch: false })}
      clean={(w) => omitEmpty({ ...w, name: w.name.trim(), address: w.address.trim(), peer_public_key: w.peer_public_key.trim(), endpoint: w.endpoint.trim() })}
      validate={(w) => validateWireGuardClient(w)}
      columns={[
        { label: 'Endpoint', render: (w) => w.endpoint },
        { label: 'Address', render: (w) => <code>{w.address}</code> },
        { label: 'Enabled', render: (w) => (w.enabled ? 'yes' : 'no') },
      ]}
      rowActions={(w) => (
        <>
          <button type="button" aria-label={`Show public key for ${w.name}`} onClick={() => setKeyModal({ name: w.name, mode: 'show' })} className="text-blue-600 hover:underline">
            public key
          </button>
          <button type="button" aria-label={`Paste private key for ${w.name}`} onClick={() => setKeyModal({ name: w.name, mode: 'paste' })} className="ml-2 text-blue-600 hover:underline">
            private key
          </button>
        </>
      )}
      after={() => keyModal && <KeyModal name={keyModal.name} mode={keyModal.mode} onClose={() => setKeyModal(null)} />}
      form={(w, set, { err }) => (
        <>
          <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input type="checkbox" checked={w.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
            Enabled
          </label>
          <label className="block">
            Tunnel address
            <input aria-label="Tunnel address" className={inputClass} placeholder="10.64.12.34/32" value={w.address} onChange={(e) => set({ address: e.target.value })} />
            {err('address')}
          </label>
          <label className="block">
            Peer public key
            <input aria-label="Peer public key" className={inputClass + ' font-mono'} placeholder="base64, 32 bytes" value={w.peer_public_key} onChange={(e) => set({ peer_public_key: e.target.value })} />
            {err('peer_public_key')}
          </label>
          <label className="block">
            Endpoint
            <input aria-label="Endpoint" className={inputClass} placeholder="se-sto-wg-001.example.net:51820" value={w.endpoint} onChange={(e) => set({ endpoint: e.target.value })} />
            {err('endpoint')}
          </label>
          <ListInput label="Allowed IPs" value={w.allowed_ips} onChange={(v) => set({ allowed_ips: v })} placeholder="0.0.0.0/0" />
          <ListInput label="DNS" value={w.dns} onChange={(v) => set({ dns: v })} placeholder="one per line" />
          <div className="grid grid-cols-2 gap-2">
            <label className="block">
              Keepalive (s)
              <input aria-label="Keepalive" type="number" min={0} className={inputClass} value={w.keepalive ?? ''} onChange={(e) => set({ keepalive: e.target.value === '' ? undefined : Number(e.target.value) })} />
            </label>
            <label className="block">
              MTU
              <input aria-label="MTU" type="number" min={1280} max={1500} className={inputClass} value={w.mtu ?? ''} onChange={(e) => set({ mtu: e.target.value === '' ? undefined : Number(e.target.value) })} />
              {err('mtu')}
            </label>
          </div>
          <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input type="checkbox" checked={!!w.kill_switch} onChange={(e) => set({ kill_switch: e.target.checked })} />
            Kill switch (drop traffic while the tunnel is down)
          </label>
        </>
      )}
    />
  )
}
