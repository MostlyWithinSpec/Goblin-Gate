import { BrowserRouter as Router, Navigate, Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import Login from './pages/Login'
import SetupWizard from './pages/setup/SetupWizard'
import SetupGate from './components/SetupGate'
import Network from './pages/Network'
import Policy from './pages/Policy'
import Schedules from './pages/policy/Schedules'
import Security from './pages/Security'
import Log from './pages/Log'
import PacketCapture from './pages/log/PacketCapture'
import Live from './pages/log/Live'
import System from './pages/System'
import { Revisions } from './pages/Revisions'
import Notifications from './pages/system/Notifications'
import Plugins from './pages/system/Plugins'
import PluginPage from './pages/system/PluginPage'
import Updates from './pages/system/Updates'
import GeneratedConfigPage from './pages/system/GeneratedConfig'
import Snapshots from './pages/system/Snapshots'
import SentryQueue from './pages/plugins/sentry/Queue'
import SentryIncidents from './pages/plugins/sentry/Incidents'
import SentryIncidentDetail from './pages/plugins/sentry/IncidentDetail'
import SentryRules from './pages/plugins/sentry/Rules'
import SentryBlocks from './pages/plugins/sentry/Blocks'
import Learning from './pages/plugins/sentry/Learning'
import Interfaces from './pages/network/Interfaces'
import Addresses from './pages/network/Addresses'
import AddressGroups from './pages/network/AddressGroups'
import Services from './pages/network/Services'
import ServiceGroups from './pages/network/ServiceGroups'
import PortForwards from './pages/network/PortForwards'
import UPnP from './pages/network/UPnP'
import DHCP from './pages/network/DHCP'
import DNS from './pages/network/DNS'
import Gateways from './pages/network/Gateways'
import GatewayGroups from './pages/network/GatewayGroups'
import StaticRoutes from './pages/network/StaticRoutes'
import Devices from './pages/network/Devices'
import ThreatFeeds from './pages/security/ThreatFeeds'
import DNSFilterProfiles from './pages/security/DNSFilterProfiles'
import WebFilterProfiles from './pages/security/WebFilterProfiles'
import AppControl from './pages/security/AppControl'
import IPS from './pages/security/IPS'
import Tailscale from './pages/Tailscale'
import VpnWireGuard from './pages/VpnWireGuard'
import VpnProviders from './pages/VpnProviders'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Create a client
const queryClient = new QueryClient()

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router>
        <SetupGate />
        <div className="app">
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/setup" element={<SetupWizard />} />
            <Route path="/" element={<Layout />}>
              <Route index element={<Dashboard />} />
              <Route path="/network" element={<Network />}>
                <Route index element={<Navigate to="interfaces" replace />} />
                <Route path="interfaces" element={<Interfaces />} />
                <Route path="addresses" element={<Addresses />} />
                <Route path="address-groups" element={<AddressGroups />} />
                <Route path="services" element={<Services />} />
                <Route path="service-groups" element={<ServiceGroups />} />
                <Route path="port-forwards" element={<PortForwards />} />
                <Route path="upnp" element={<UPnP />} />
                <Route path="dhcp" element={<DHCP />} />
                <Route path="dns" element={<DNS />} />
                <Route path="gateways" element={<Gateways />} />
                <Route path="gateway-groups" element={<GatewayGroups />} />
                <Route path="static-routes" element={<StaticRoutes />} />
                <Route path="devices" element={<Devices />} />
              </Route>
              <Route path="/policy" element={<Policy />} />
              <Route path="/policy/schedules" element={<Schedules />} />
              <Route path="/security" element={<Security />} />
              <Route path="/security/feeds" element={<ThreatFeeds />} />
              <Route path="/security/dns-filter" element={<DNSFilterProfiles />} />
              <Route path="/security/web-filter" element={<WebFilterProfiles />} />
              <Route path="/security/apps" element={<AppControl />} />
              <Route path="/security/ips" element={<IPS />} />
              <Route path="/vpn/tailscale" element={<Tailscale />} />
              <Route path="/vpn/wireguard" element={<VpnWireGuard />} />
              <Route path="/vpn/providers" element={<VpnProviders />} />
              <Route path="/log" element={<Log />} />
              <Route path="/log/capture" element={<PacketCapture />} />
              <Route path="/log/live" element={<Live />} />
              <Route path="/system" element={<System />} />
              <Route path="/system/revisions" element={<Revisions />} />
              <Route path="/system/notifications" element={<Notifications />} />
              <Route path="/system/plugins" element={<Plugins />} />
              <Route path="/system/plugins/:name/*" element={<PluginPage />} />
              <Route path="/system/updates" element={<Updates />} />
              <Route path="/system/generated" element={<GeneratedConfigPage />} />
              <Route path="/system/snapshots" element={<Snapshots />} />
              <Route path="/plugins/sentry" element={<SentryQueue />} />
              <Route path="/plugins/sentry/incidents" element={<SentryIncidents />} />
              <Route path="/plugins/sentry/incidents/:id" element={<SentryIncidentDetail />} />
              <Route path="/plugins/sentry/rules" element={<SentryRules />} />
              <Route path="/plugins/sentry/blocks" element={<SentryBlocks />} />
              <Route path="/plugins/sentry/learning" element={<Learning />} />
            </Route>
          </Routes>
        </div>
      </Router>
    </QueryClientProvider>
  )
}

export default App