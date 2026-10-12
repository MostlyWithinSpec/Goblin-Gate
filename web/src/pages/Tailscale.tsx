import React, { useState, useEffect } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Button } from '../components/Button'
import { Badge } from '../components/Badge'
import { DataTable } from '../components/DataTable'
import { EditDrawer } from '../components/EditDrawer'
import { FormField } from '../components/FormField'
import { TagInput } from '../components/TagInput'
import { getTailscaleStatus, loginTailscale, logoutTailscale, patchTailscaleSettings, TailscaleStatus, TailscalePeer, TailscaleSettings } from '../api/tailscale'
import { getNetworkConfig } from '../api/network'
import { NetworkConfig } from '../api/network'

const TailscalePage: React.FC = () => {
  const [status, setStatus] = useState<TailscaleStatus | null>(null)
  const [isLoginModalOpen, setIsLoginModalOpen] = useState(false)
  const [authKey, setAuthKey] = useState('')
  const [isSettingsOpen, setIsSettingsOpen] = useState(false)
  const [settings, setSettings] = useState<TailscaleSettings>({
    enabled: false,
    hostname: '',
    advertise_routes: [],
    exit_node: false,
    accept_routes: false,
    accept_dns: false,
    login_server: '',
    allow_access: []
  })
  const [loginStatus, setLoginStatus] = useState<'idle' | 'logging_in' | 'success' | 'error'>('idle')
  const [loginError, setLoginError] = useState<string | null>(null)
  const [logoutStatus, setLogoutStatus] = useState<'idle' | 'logging_out' | 'success' | 'error'>('idle')
  const [isLoginButtonDisabled, setIsLoginButtonDisabled] = useState(false)

  const queryClient = useQueryClient()

  // Fetch Tailscale status
  const { isLoading, error } = useQuery({
    queryKey: ['tailscaleStatus'],
    queryFn: getTailscaleStatus,
    onSuccess: (data) => {
      setStatus(data)
    },
    refetchInterval: 5000, // Auto-refresh every 5 seconds
  })

  // Fetch network config to get available interfaces for advertise_routes
  const { data: networkConfig } = useQuery<NetworkConfig>({
    queryKey: ['networkConfig'],
    queryFn: getNetworkConfig
  })

  // Handle login
  const handleLogin = async () => {
    setLoginStatus('logging_in')
    setLoginError(null)
    
    try {
      if (authKey) {
        // Login with auth key
        await loginTailscale(authKey)
        setAuthKey('')
        setIsLoginModalOpen(false)
        setLoginStatus('success')
      } else {
        // Login interactively - this will return auth_url
        const result = await loginTailscale()
        if (result.auth_url) {
          // Open the auth URL in a new tab
          window.open(result.auth_url, '_blank')
          setLoginStatus('success')
          setIsLoginModalOpen(false)
        } else {
          setLoginStatus('success')
          setIsLoginModalOpen(false)
        }
      }
    } catch (err) {
      setLoginError('Failed to initiate login. Please try again.')
      setLoginStatus('error')
      console.error('Login error:', err)
    } finally {
      // Reset login status after a short delay
      setTimeout(() => {
        setLoginStatus('idle')
      }, 3000)
    }
  }

  // Handle logout
  const handleLogout = async () => {
    setLogoutStatus('logging_out')
    try {
      await logoutTailscale()
      setLogoutStatus('success')
    } catch (err) {
      console.error('Logout error:', err)
      setLogoutStatus('error')
    } finally {
      // Reset logout status after a short delay
      setTimeout(() => {
        setLogoutStatus('idle')
      }, 3000)
    }
  }

  // Handle settings save
  const handleSaveSettings = async () => {
    try {
      await patchTailscaleSettings(settings)
      setIsSettingsOpen(false)
      // Invalidate cache to force refetch
      queryClient.invalidateQueries(['tailscaleStatus'])
      queryClient.invalidateQueries(['networkConfig'])
    } catch (err) {
      console.error('Error saving settings:', err)
    }
  }

  // Update settings when status changes
  useEffect(() => {
    if (status && status.available) {
      setSettings({
        enabled: true,
        hostname: status.hostname || '',
        advertise_routes: status.advertised_routes || [],
        exit_node: status.approved_routes?.includes('0.0.0.0/0') || false,
        accept_routes: false, // Placeholder - this would need to be set from the API
        accept_dns: false, // Placeholder - this would need to be set from the API
        login_server: '', // Placeholder - this would need to be set from the API
        allow_access: [] // Placeholder - this would need to be set from the API
      })
    } else {
      setSettings({
        enabled: false,
        hostname: '',
        advertise_routes: [],
        exit_node: false,
        accept_routes: false,
        accept_dns: false,
        login_server: '',
        allow_access: []
      })
    }
  }, [status])

  // Status badge component
  const StatusBadge: React.FC<{ status: string }> = ({ status }) => {
    switch (status) {
      case 'Running':
        return <Badge variant="success">Connected</Badge>
      case 'Needs login':
        return <Badge variant="warning">Needs login</Badge>
      case 'Not installed':
        return <Badge variant="error">Not installed</Badge>
      default:
        return <Badge variant="secondary">Unknown</Badge>
    }
  }

  // Get the status text
  const getStatusText = () => {
    if (!status) return 'Unknown'
    if (!status.available) return 'Not installed'
    if (status.backend_state === 'Running') return 'Connected'
    if (status.backend_state === 'NeedsLogin') return 'Needs login'
    return status.backend_state || 'Unknown'
  }

  // Get the status variant for badge
  const getStatusVariant = () => {
    if (!status) return 'secondary'
    if (!status.available) return 'error'
    if (status.backend_state === 'Running') return 'success'
    if (status.backend_state === 'NeedsLogin') return 'warning'
    return 'secondary'
  }

  // Prepare data for the peers table
  const peersData = status?.peers?.map((peer) => ({
    name: peer.name,
    ips: peer.ips.join(', '),
    os: peer.os || 'Unknown',
    online: peer.online ? 'Online' : 'Offline',
    lastSeen: peer.last_seen ? new Date(peer.last_seen).toLocaleString() : 'Never',
    exitNode: peer.exit_node ? 'Yes' : 'No',
    exitNodeOption: peer.exit_node_option ? 'Yes' : 'No',
    routes: peer.routes?.join(', ') || 'None'
  })) || []

  // Prepare columns for the peers table
  const peersColumns = [
    { key: 'name', label: 'Name' },
    { key: 'ips', label: 'IP Addresses' },
    { key: 'os', label: 'OS' },
    { key: 'online', label: 'Status' },
    { key: 'lastSeen', label: 'Last Seen' },
    { key: 'exitNode', label: 'Exit Node' },
    { key: 'exitNodeOption', label: 'Exit Node Option' },
    { key: 'routes', label: 'Routes' }
  ]

  if (isLoading) {
    return <div className="p-6">Loading Tailscale status...</div>
  }

  if (error) {
    return <div className="p-6">Error loading Tailscale status: {(error as Error).message}</div>
  }

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">Tailscale</h1>
        <div className="flex space-x-2">
          <Button variant="outline" onClick={() => setIsLoginModalOpen(true)}>
            Login
          </Button>
          <Button variant="outline" onClick={() => setIsSettingsOpen(true)}>
            Settings
          </Button>
        </div>
      </div>

      {/* Status Card */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6 mb-6">
        <div className="flex flex-col md:flex-row md:items-center md:justify-between">
          <div>
            <h2 className="text-lg font-semibold mb-2">Tailscale Status</h2>
            <div className="flex items-center space-x-4">
              <StatusBadge status={getStatusText()} />
              <span className="text-gray-600 dark:text-gray-300">
                {status?.hostname ? `Hostname: ${status.hostname}` : 'Hostname: Unknown'}
              </span>
              {status?.ips && status.ips.length > 0 && (
                <span className="text-gray-600 dark:text-gray-300">
                  IPs: {status.ips.join(', ')}
                </span>
              )}
            </div>
          </div>
          <div className="mt-4 md:mt-0 flex space-x-2">
            <Button 
              variant="outline" 
              onClick={handleLogout}
              disabled={logoutStatus === 'logging_out'}
            >
              {logoutStatus === 'logging_out' ? 'Logging out...' : 'Logout'}
            </Button>
          </div>
        </div>
      </div>

      {/* Login Modal */}
      {isLoginModalOpen && (
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
          <div className="bg-white dark:bg-gray-800 rounded-lg p-6 w-full max-w-md">
            <h2 className="text-xl font-bold mb-4">Login to Tailscale</h2>
            <div className="mb-4">
              <FormField label="Auth Key (optional)">
                <input
                  type="password"
                  value={authKey}
                  onChange={(e) => setAuthKey(e.target.value)}
                  className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700"
                  placeholder="Enter auth key (tskey-...)"
                />
              </FormField>
            </div>
            <div className="flex justify-end space-x-2">
              <Button variant="outline" onClick={() => setIsLoginModalOpen(false)}>
                Cancel
              </Button>
              <Button 
                variant="primary" 
                onClick={handleLogin}
                disabled={loginStatus === 'logging_in'}
              >
                {loginStatus === 'logging_in' ? 'Logging in...' : 'Login'}
              </Button>
            </div>
            {loginError && (
              <div className="mt-4 p-2 bg-red-100 text-red-700 rounded">
                {loginError}
              </div>
            )}
          </div>
        </div>
      )}

      {/* Settings Drawer */}
      {isSettingsOpen && (
        <EditDrawer
          isOpen={isSettingsOpen}
          onClose={() => setIsSettingsOpen(false)}
          title="Tailscale Settings"
        >
          <div className="space-y-4">
            <div className="flex items-center">
              <input
                type="checkbox"
                id="enabled"
                checked={settings.enabled}
                onChange={(e) => setSettings({...settings, enabled: e.target.checked})}
                className="mr-2"
              />
              <label htmlFor="enabled">Enable Tailscale</label>
            </div>
            
            <FormField label="Hostname">
              <input
                type="text"
                value={settings.hostname || ''}
                onChange={(e) => setSettings({...settings, hostname: e.target.value})}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700"
                placeholder="Enter hostname"
              />
            </FormField>
            
            <FormField label="Advertise Routes">
              <TagInput
                value={settings.advertise_routes || []}
                onChange={(value) => setSettings({...settings, advertise_routes: value})}
                placeholder="Enter interface/VLAN names"
              />
            </FormField>
            
            <div className="flex items-center">
              <input
                type="checkbox"
                id="exit_node"
                checked={settings.exit_node}
                onChange={(e) => setSettings({...settings, exit_node: e.target.checked})}
                className="mr-2"
              />
              <label htmlFor="exit_node">Offer as Exit Node</label>
            </div>
            
            <div className="flex items-center">
              <input
                type="checkbox"
                id="accept_routes"
                checked={settings.accept_routes}
                onChange={(e) => setSettings({...settings, accept_routes: e.target.checked})}
                className="mr-2"
              />
              <label htmlFor="accept_routes">Accept Routes</label>
            </div>
            
            <div className="flex items-center">
              <input
                type="checkbox"
                id="accept_dns"
                checked={settings.accept_dns}
                onChange={(e) => setSettings({...settings, accept_dns: e.target.checked})}
                className="mr-2"
              />
              <label htmlFor="accept_dns">Accept DNS</label>
            </div>
            
            <FormField label="Login Server">
              <input
                type="text"
                value={settings.login_server || ''}
                onChange={(e) => setSettings({...settings, login_server: e.target.value})}
                className="w-full p-2 border border-gray-300 dark:border-gray-600 rounded bg-white dark:bg-gray-700"
                placeholder="Enter Headscale URL"
              />
            </FormField>
            
            <FormField label="Allow Access">
              <TagInput
                value={settings.allow_access || []}
                onChange={(value) => setSettings({...settings, allow_access: value})}
                placeholder="Enter access types (ping, https, ssh)"
              />
            </FormField>
            
            <div className="flex justify-end space-x-2 mt-6">
              <Button variant="outline" onClick={() => setIsSettingsOpen(false)}>
                Cancel
              </Button>
              <Button variant="primary" onClick={handleSaveSettings}>
                Save Settings
              </Button>
            </div>
          </div>
        </EditDrawer>
      )}

      {/* Peers Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm p-6">
        <h2 className="text-lg font-semibold mb-4">Peers</h2>
        <DataTable
          data={peersData}
          columns={peersColumns}
          searchable={true}
          searchFields={['name', 'ips']}
        />
      </div>
    </div>
  )
}

export default TailscalePage