import React, { useState, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { DataTable } from '../components/DataTable'
import { EditDrawer } from '../components/EditDrawer'
import { FormField } from '../components/FormField'
import { getWireGuardConfig, addWireGuardPeer, WireGuardPeerRequest, WireGuardPeerResponse } from '../api/wireguard'
import { Link } from 'react-router-dom'

const WireGuardPage: React.FC = () => {
  const [isAddPeerOpen, setIsAddPeerOpen] = useState(false)
  const [selectedInterface, setSelectedInterface] = useState<string | null>(null)
  const [peerName, setPeerName] = useState('')
  const [fullTunnel, setFullTunnel] = useState(false)
  const [keepalive, setKeepalive] = useState<number>(0)
  const [isAddPeerModalOpen, setIsAddPeerModalOpen] = useState(false)
  const [peerConfig, setPeerConfig] = useState<string | null>(null)
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
      setAddPeerError('Please select an interface and provide a peer name')
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
      console.error('Error adding peer:', err)
      setAddPeerError('Failed to add peer. Please try again.')
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
    if (!time || time === '0') return 'Never'
    return time
  }

  if (isLoading) {
    return <div className="p-6">Loading WireGuard configuration...</div>
  }

  if (error) {
    return <div className="p-6">Error loading WireGuard configuration: {(error as Error).message}</div>
  }

  // Prepare data for the interfaces table
  const interfacesData = wireGuardConfig?.interfaces?.map((iface) => ({
    name: iface.name,
    enabled: iface.enabled ? 'Enabled' : 'Disabled',
    publicKey: iface.public_key,
    listenPort: iface.listen_port,
    address: iface.address,
    endpoint: iface.endpoint || 'N/A',
    peers: iface.peers.length.toString()
  })) || []

  // Prepare columns for the interfaces table
  const interfacesColumns = [
    { key: 'name', label: 'Name' },
    { key: 'enabled', label: 'Status' },
    { key: 'publicKey', label: 'Public Key' },
    { key: 'listenPort', label: 'Listen Port' },
    { key: 'address', label: 'Address' },
    { key: 'endpoint', label: 'Endpoint' },
    { key: 'peers', label: 'Peers' }
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
    { key: 'interface', label: 'Interface' },
    { key: 'name', label: 'Name' },
    { key: 'address', label: 'Address' },
    { key: 'lastHandshake', label: 'Last Handshake' },
    { key: 'rxBytes', label: 'Received' },
    { key: 'txBytes', label: 'Sent' }
  ]

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">WireGuard</h1>
        <div className="flex space-x-2">
          <Button variant="primary" onClick={() => setIsAddPeerOpen(true)}>
            Add Peer
          </Button>
        </div>
      </div>

      {/* Interfaces Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6 mb-6">
        <h2 className="text-lg font-semibold mb-4">WireGuard Interfaces</h2>
        <DataTable
          data={interfacesData}
          columns={interfacesColumns}
          searchable={true}
          searchFields={['name', 'address']}
        />
      </div>

      {/* Peers Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6">
        <h2 className="text-lg font-semibold mb-4">Peers</h2>
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
          title="Add WireGuard Peer"
        >
          <div className="space-y-4">
            <FormField label="Interface">
              <select
                value={selectedInterface || ''}
                onChange={(e) => setSelectedInterface(e.target.value)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
              >
                <option value="">Select an interface</option>
                {wireGuardConfig?.interfaces.map((iface) => (
                  <option key={iface.name} value={iface.name}>
                    {iface.name}
                  </option>
                ))}
              </select>
            </FormField>

            <FormField label="Peer Name">
              <input
                type="text"
                value={peerName}
                onChange={(e) => setPeerName(e.target.value)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
                placeholder="Enter peer name"
              />
            </FormField>

            <FormField label="Keepalive (seconds)">
              <input
                type="number"
                min="0"
                value={keepalive}
                onChange={(e) => setKeepalive(parseInt(e.target.value) || 0)}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700 min-h-[40px]"
                placeholder="0 = off"
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
              <label htmlFor="full-tunnel">Send all traffic through Goblin Gate</label>
            </div>

            <div className="flex justify-end space-x-2 mt-6">
              <Button variant="outline" onClick={() => setIsAddPeerOpen(false)}>
                Cancel
              </Button>
              <Button 
                variant="primary" 
                onClick={() => {
                  setIsAddPeerModalOpen(true)
                  setIsAddPeerOpen(false)
                }}
                disabled={!selectedInterface || !peerName}
              >
                Add Peer
              </Button>
            </div>
          </div>
        </EditDrawer>
      )}

      {/* Add Peer Confirmation Modal */}
      {isAddPeerModalOpen && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white dark:bg-gray-800 rounded-lg p-6 w-full max-w-md">
            <h2 className="text-xl font-bold mb-4">Confirm Add Peer</h2>
            <p className="mb-4">
              Are you sure you want to add peer <strong>{peerName}</strong> to interface <strong>{selectedInterface}</strong>?
            </p>
            <div className="flex justify-end space-x-2">
              <Button variant="outline" onClick={() => setIsAddPeerModalOpen(false)}>
                Cancel
              </Button>
              <Button 
                variant="primary" 
                onClick={handleAddPeer}
                disabled={isAddingPeer}
              >
                {isAddingPeer ? 'Adding...' : 'Add Peer'}
              </Button>
            </div>
            {addPeerError && (
              <div className="mt-4 p-2 bg-red-100 text-red-700 rounded">
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
            <h2 className="text-xl font-bold mb-4">Peer Configuration</h2>
            
            {qrCode && (
              <div className="mb-4 bg-white p-4 rounded-lg">
                <h3 className="font-semibold mb-2">QR Code</h3>
                <div className="bg-white p-2 inline-block">
                  <div dangerouslySetInnerHTML={{ __html: qrCode }} />
                </div>
              </div>
            )}
            
            <div className="mb-4">
              <h3 className="font-semibold mb-2">Configuration File</h3>
              <div className="bg-gray-100 dark:bg-gray-700 p-4 rounded-lg overflow-x-auto">
                <pre className="text-sm">{peerConfig}</pre>
              </div>
            </div>
            
            <div className="mb-4 p-3 bg-yellow-100 dark:bg-yellow-900 rounded">
              <p className="text-sm">
                <strong>Important:</strong> The private key is shown only once in this configuration file. 
                Make sure to save it securely. The private key is never stored by Goblin Gate.
              </p>
            </div>
            
            <div className="flex justify-end space-x-2">
              <Button variant="outline" onClick={() => setShowPeerConfig(false)}>
                Close
              </Button>
              <a 
                href={`data:text/plain;charset=utf-8,${encodeURIComponent(peerConfig)}`} 
                download={`${peerName}.conf`}
                className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700"
              >
                Download .conf
              </a>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default WireGuardPage