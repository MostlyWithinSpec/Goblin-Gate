import React, { useState, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { DataTable } from '../components/DataTable'
import { EditDrawer } from '../components/EditDrawer'
import { FormField } from '../components/FormField'
import { getWireGuardConfig, addWireGuardPeer, WireGuardPeerRequest, WireGuardPeerResponse } from '../api/wireguard'
import { Link } from 'react-router-dom'
import { t } from '../i18n'

const WireGuardPage: React.FC = () => {
  const [isAddPeerOpen, setIsAddPeerOpen] = useState(false)
  const [selectedInterface, setSelectedInterface] = useState<string | null>(null)
  const [peerName, setPeerName] = useState('')
  const [fullTunnel, setFullTunnel] = useState(false)
  const [keepalive, setKeepalive] = useState<number>(0)
  const [isAddPeerModalOpen, setIsAddPeerModalOpen] = useState(false)
  const [peerConfig, setPeerConfig] = useState<string | null>(null)
  // the added peer's name, for the .conf download (the form is reset by then)
  const [peerConfigName, setPeerConfigName] = useState('peer')
  const [qrCode, setQrCode] = useState<string | null>(null)
  const [showPeerConfig, setShowPeerConfig] = useState(false)
  const [isAddingPeer, setIsAddingPeer] = useState(false)
  const [addPeerError, setAddPeerError] = useState<string | null>(null)

  const queryClient = useQueryClient()

  // Fetch WireGuard configuration
  const { data: wireGuardConfig, isLoading, error } = useQuery({
    queryKey: ['wireguardConfig'],
    queryFn: getWireGuardConfig,
    refetchInterval: 5000, // Auto-refresh every 5 seconds
  })

  // Handle adding a new peer
  const handleAddPeer = async () => {
    if (!selectedInterface || !peerName) {
      setAddPeerError(t('wireGuard.selectInterfaceAndName'))
      return
    }

    setIsAddingPeer(true)
    setAddPeerError(null)
    
    try {
      const request: WireGuardPeerRequest = {
        name: peerName,
        full_tunnel: fullTunnel,
        keepalive: keepalive > 0 ? keepalive : undefined
      }
      
      const response: WireGuardPeerResponse = await addWireGuardPeer(selectedInterface, request)
      
      setPeerConfig(response.config)
      setPeerConfigName(peerName)
      setQrCode(response.qr_svg || null)
      setShowPeerConfig(true)
      setIsAddPeerOpen(false)
      setIsAddPeerModalOpen(false)
      setPeerName('')
      setFullTunnel(false)
      setKeepalive(0)
      
      // Invalidate cache to refresh the list
      queryClient.invalidateQueries(['wireguardConfig'])
    } catch (err) {
      setAddPeerError(t('wireGuard.addPeerFailed', { message: (err as Error).message }))
    } finally {
      setIsAddingPeer(false)
    }
  }

  // Format bytes for display
  const formatBytes = (bytes?: number): string => {
    if (bytes === undefined || bytes === 0) return '0 B'
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return `${(bytes / Math.pow(1024, i)).toFixed(2)} ${sizes[i]}`
  }

  // Format time for display
  const formatTime = (time?: string): string => {
    if (!time || time === '0') return t('wireGuard.never')
    return time
  }

  if (isLoading) {
    return <div className="p-6">{t('wireGuard.loadingConfig')}</div>
  }

  if (error) {
    return <div className="p-6">{t('wireGuard.loadingConfigError', { message: (error as Error).message })}</div>
  }

  // Prepare data for the interfaces table
  const interfacesData = wireGuardConfig?.interfaces?.map((iface) => ({
    name: iface.name,
    enabled: iface.enabled ? t('wireGuard.enabled') : t('wireGuard.disabled'),
    publicKey: iface.public_key,
    listenPort: iface.listen_port,
    address: iface.address,
    endpoint: iface.endpoint || 'N/A',
    peers: iface.peers.length.toString()
  })) || []

  // Prepare columns for the interfaces table
  const interfacesColumns = [
    { key: 'name', label: t('wireGuard.col.name') },
    { key: 'enabled', label: t('wireGuard.col.status') },
    { key: 'publicKey', label: t('wireGuard.col.publicKey') },
    { key: 'listenPort', label: t('wireGuard.col.listenPort') },
    { key: 'address', label: t('wireGuard.col.address') },
    { key: 'endpoint', label: t('wireGuard.col.endpoint') },
    { key: 'peers', label: t('wireGuard.col.peers') }
  ]

  // Prepare data for the peers table
  const peersData = wireGuardConfig?.interfaces.flatMap((iface) => 
    iface.peers.map((peer) => ({
      interface: iface.name,
      name: peer.name,
      address: peer.address,
      lastHandshake: formatTime(peer.latest_handshake),
      rxBytes: formatBytes(peer.rx_bytes),
      txBytes: formatBytes(peer.tx_bytes),
      publicKey: peer.public_key
    }))
  ) || []

  // Prepare columns for the peers table
  const peersColumns = [
    { key: 'interface', label: t('wireGuard.col.interface') },
    { key: 'name', label: t('wireGuard.col.name') },
    { key: 'address', label: t('wireGuard.col.address') },
    { key: 'lastHandshake', label: t('wireGuard.col.lastHandshake') },
    { key: 'rxBytes', label: t('wireGuard.col.received') },
    { key: 'txBytes', label: t('wireGuard.col.sent') }
  ]

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">{t('wireGuard.title')}</h1>
        <div className="flex space-x-2">
          <Button variant="primary" onClick={() => setIsAddPeerOpen(true)}>
            {t('wireGuard.addPeer')}
          </Button>
        </div>
      </div>

      {/* Interfaces Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">{t('wireGuard.interfaces')}</h2>
        <DataTable
          data={interfacesData}
          columns={interfacesColumns}
          searchable={true}
          searchFields={['name', 'address']}
        />
      </div>

      {/* Peers Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6">
        <h2 className="text-lg font-semibold mb-4">{t('wireGuard.peers')}</h2>
        <DataTable
          data={peersData}
          columns={peersColumns}
          searchable={true}
          searchFields={['name', 'address']}
        />
      </div>

      {/* Add Peer Drawer */}
      {isAddPeerOpen && (
        <EditDrawer
          isOpen={isAddPeerOpen}
          onClose={() => setIsAddPeerOpen(false)}
          title={t('wireGuard.addPeerDrawer')}
        >
          <div className="space-y-4">
            <FormField label={t('wireGuard.interface')}>
              <select
                value={selectedInterface || ''}
                onChange={(e) => setSelectedInterface(e.target.value)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
              >
                <option value="">{t('wireGuard.selectInterface')}</option>
                {wireGuardConfig?.interfaces.map((iface) => (
                  <option key={iface.name} value={iface.name}>
                    {iface.name}
                  </option>
                ))}
              </select>
            </FormField>

            <FormField label={t('wireGuard.peerName')}>
              <input
                type="text"
                value={peerName}
                onChange={(e) => setPeerName(e.target.value)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
                placeholder={t('wireGuard.peerNamePlaceholder')}
              />
            </FormField>

            <FormField label={t('wireGuard.keepaliveSeconds')}>
              <input
                type="number"
                min="0"
                value={keepalive}
                onChange={(e) => setKeepalive(parseInt(e.target.value) || 0)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
                placeholder={t('wireGuard.keepalivePlaceholder')}
              />
            </FormField>

            <div className="flex items-center">
              <input
                type="checkbox"
                id="full-tunnel"
                checked={fullTunnel}
                onChange={(e) => setFullTunnel(e.target.checked)}
                className="mr-2"
              />
              <label htmlFor="full-tunnel">{t('wireGuard.fullTunnel')}</label>
            </div>

            <div className="flex justify-end space-x-2 mt-6">
              <Button variant="outline" onClick={() => setIsAddPeerOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="primary"
                onClick={() => {
                  setIsAddPeerModalOpen(true)
                  setIsAddPeerOpen(false)
                }}
                disabled={!selectedInterface || !peerName}
              >
                {t('wireGuard.addPeer')}
              </Button>
            </div>
          </div>
        </EditDrawer>
      )}

      {/* Add Peer Confirmation Modal */}
      {isAddPeerModalOpen && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white dark:bg-gray-800 rounded-lg p-6 w-full max-w-md">
            <h2 className="text-xl font-bold mb-4">{t('wireGuard.confirmAddPeer')}</h2>
            <p className="mb-4">
              {t('wireGuard.confirmAddPeerBody')} <strong>{peerName}</strong> {t('wireGuard.confirmAddPeerTo')} <strong>{selectedInterface}</strong>?
            </p>
            <div className="flex justify-end space-x-2">
              <Button variant="outline" onClick={() => setIsAddPeerModalOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="primary"
                onClick={handleAddPeer}
                disabled={isAddingPeer}
              >
                {isAddingPeer ? t('wireGuard.adding') : t('wireGuard.addPeer')}
              </Button>
            </div>
            {addPeerError && (
              <div role="alert" className="mt-4 p-2 bg-red-100 text-red-700 rounded">
                {addPeerError}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Peer Config Modal */}
      {showPeerConfig && peerConfig && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white dark:bg-gray-800 rounded-lg p-6 w-full max-w-2xl max-h-[90vh] overflow-y-auto">
            <h2 className="text-xl font-bold mb-4">{t('wireGuard.peerConfig')}</h2>

            {qrCode && (
              <div className="mb-4 bg-white p-4 rounded-lg">
                <h3 className="font-semibold mb-2">{t('wireGuard.qrCode')}</h3>
                <div className="bg-white p-2 inline-block">
                  <div dangerouslySetInnerHTML={{ __html: qrCode }} />
                </div>
              </div>
            )}

            <div className="mb-4">
              <h3 className="font-semibold mb-2">{t('wireGuard.configFile')}</h3>
              <div className="bg-gray-100 dark:bg-gray-700 p-4 rounded-lg overflow-x-auto">
                <pre className="text-sm">{peerConfig}</pre>
              </div>
            </div>

            <div className="mb-4 p-3 bg-yellow-100 dark:bg-yellow-900 rounded">
              <p className="text-sm">
                <strong>{t('wireGuard.important')}</strong> {t('wireGuard.privateKeyOnce')} {t('wireGuard.privateKeyRest')}
              </p>
            </div>

            <div className="flex justify-end space-x-2">
              <Button variant="outline" onClick={() => setShowPeerConfig(false)}>
                {t('common.close')}
              </Button>
              <a
                href={`data:text/plain;charset=utf-8,${encodeURIComponent(peerConfig)}`}
                download={`${peerConfigName}.conf`}
                className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700"
              >
                {t('wireGuard.downloadConf')}
              </a>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default WireGuardPage