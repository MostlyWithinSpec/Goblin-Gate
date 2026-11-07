// VPN providers (config "vpn.wireguard_clients"): WireGuard tunnels to a
// provider that act as gateways. The private key is stored outside the
// config; here you show the generated public key or paste the provider's key.
import { useEffect, useState } from 'react'
import { ConfigList, inputClass, ListInput, omitEmpty } from '../components/ConfigList'
import type { WireGuardClient } from '../api/objects'
import { getWireGuardClientPublicKey, setWireGuardClientPrivateKey } from '../api/gateways'
import { validateWireGuardClient } from './network/networkLogic'
import { t } from '../i18n'

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
          {(mode === 'show' ? t('vpnProviders.publicKeyTitle') : t('vpnProviders.pastePrivateKeyTitle')) + ' — ' + name}
        </h3>
        {busy && <p className="text-sm text-gray-500">{t('vpnProviders.working')}</p>}
        {mode === 'paste' && !result && (
          <label className="block text-sm text-gray-700 dark:text-gray-300">
            {t('vpnProviders.privateKey')}
            <textarea
              aria-label={t('vpnProviders.privateKey')}
              rows={3}
              className={inputClass + ' font-mono'}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={t('vpnProviders.privateKeyPlaceholder')}
            />
          </label>
        )}
        {result && <pre className="mt-2 break-all rounded bg-gray-100 p-3 font-mono text-xs dark:bg-gray-700">{result}</pre>}
        {error && <div role="alert" className="mt-2 rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
        <div className="mt-4 flex justify-end gap-2">
          {mode === 'paste' && !result && (
            <button type="button" disabled={busy || !key.trim()} onClick={paste} className="rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
              {t('vpnProviders.saveKey')}
            </button>
          )}
          <button type="button" onClick={onClose} className="rounded border border-gray-300 px-3 py-1 text-sm dark:border-gray-600 dark:text-gray-200">{t('common.close')}</button>
        </div>
      </div>
    </div>
  )
}

export default function VpnProviders() {
  const [keyModal, setKeyModal] = useState<{ name: string; mode: 'show' | 'paste' } | null>(null)

  return (
    <ConfigList<WireGuardClient>
      title={t('vpnProviders.title')}
      path="vpn.wireguard_clients"
      newLabel={t('vpnProviders.newProvider')}
      intro={t('vpnProviders.intro')}
      select={(c) => c.vpn?.wireguard_clients}
      empty={() => ({ name: '', enabled: true, address: '', peer_public_key: '', endpoint: '', kill_switch: false })}
      clean={(w) => omitEmpty({ ...w, name: w.name.trim(), address: w.address.trim(), peer_public_key: w.peer_public_key.trim(), endpoint: w.endpoint.trim() })}
      validate={(w) => validateWireGuardClient(w)}
      columns={[
        { label: t('vpnProviders.col.endpoint'), render: (w) => w.endpoint },
        { label: t('vpnProviders.col.address'), render: (w) => <code>{w.address}</code> },
        { label: t('vpnProviders.col.enabled'), render: (w) => (w.enabled ? 'yes' : 'no') },
      ]}
      rowActions={(w) => (
        <>
          <button type="button" aria-label={t('vpnProviders.showPublicKey', { name: w.name })} onClick={() => setKeyModal({ name: w.name, mode: 'show' })} className="text-blue-600 hover:underline">
            {t('vpnProviders.publicKeyLink')}
          </button>
          <button type="button" aria-label={t('vpnProviders.pastePrivateKey', { name: w.name })} onClick={() => setKeyModal({ name: w.name, mode: 'paste' })} className="ml-2 text-blue-600 hover:underline">
            {t('vpnProviders.privateKeyLink')}
          </button>
        </>
      )}
      after={() => keyModal && <KeyModal name={keyModal.name} mode={keyModal.mode} onClose={() => setKeyModal(null)} />}
      form={(w, set, { err }) => (
        <>
          <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input type="checkbox" checked={w.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
            {t('vpnProviders.enabled')}
          </label>
          <label className="block">
            {t('vpnProviders.tunnelAddress')}
            <input aria-label={t('vpnProviders.tunnelAddress')} className={inputClass} placeholder={t('vpnProviders.tunnelAddressPlaceholder')} value={w.address} onChange={(e) => set({ address: e.target.value })} />
            {err('address')}
          </label>
          <label className="block">
            {t('vpnProviders.peerPublicKey')}
            <input aria-label={t('vpnProviders.peerPublicKey')} className={inputClass + ' font-mono'} placeholder={t('vpnProviders.peerPublicKeyPlaceholder')} value={w.peer_public_key} onChange={(e) => set({ peer_public_key: e.target.value })} />
            {err('peer_public_key')}
          </label>
          <label className="block">
            {t('vpnProviders.endpoint')}
            <input aria-label={t('vpnProviders.endpoint')} className={inputClass} placeholder={t('vpnProviders.endpointPlaceholder')} value={w.endpoint} onChange={(e) => set({ endpoint: e.target.value })} />
            {err('endpoint')}
          </label>
          <ListInput label={t('vpnProviders.allowedIps')} value={w.allowed_ips} onChange={(v) => set({ allowed_ips: v })} placeholder="0.0.0.0/0" />
          <ListInput label={t('vpnProviders.dns')} value={w.dns} onChange={(v) => set({ dns: v })} placeholder={t('vpnProviders.onePerLine')} />
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label className="block">
              {t('vpnProviders.keepalive')}
              <input aria-label={t('vpnProviders.keepalive')} type="number" min={0} className={inputClass} value={w.keepalive ?? ''} onChange={(e) => set({ keepalive: e.target.value === '' ? undefined : Number(e.target.value) })} />
            </label>
            <label className="block">
              {t('vpnProviders.mtu')}
              <input aria-label={t('vpnProviders.mtu')} type="number" min={1280} max={1500} className={inputClass} value={w.mtu ?? ''} onChange={(e) => set({ mtu: e.target.value === '' ? undefined : Number(e.target.value) })} />
              {err('mtu')}
            </label>
          </div>
          <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input type="checkbox" checked={!!w.kill_switch} onChange={(e) => set({ kill_switch: e.target.checked })} />
            {t('vpnProviders.killSwitch')}
          </label>
        </>
      )}
    />
  )
}
