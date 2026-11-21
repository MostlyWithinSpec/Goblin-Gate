// Device inventory API — live state, not candidate config.
import { apiClient } from './client'

// AnnotationFields are one plugin's free-form labels for a device, such as
// the switch port or the Wi-Fi network it is on. Read-only display data.
export type AnnotationFields = Record<string, string>

// DeviceAnnotations are a device's labels, by plugin.
export type DeviceAnnotations = Record<string, AnnotationFields>

export interface DeviceInventory {
  mac: string
  ips: string[]
  hostname?: string
  vendor?: string
  interface?: string
  first_seen: string
  last_seen: string
  name?: string
  randomized?: boolean
  annotations?: DeviceAnnotations
  vlan?: string // the VLAN RADIUS puts the device in
}

// DeviceList is the answer to GET /api/v1/devices.
export interface DeviceList {
  devices: DeviceInventory[]
  // annotations_updated is when each plugin with devices:annotate last
  // replaced its annotations; absent when no plugin has that capability.
  annotations_updated?: Record<string, string>
}

export async function listDevices(interfaceFilter?: string): Promise<DeviceList> {
  const params = new URLSearchParams()
  if (interfaceFilter) params.set('interface', interfaceFilter)
  const qs = params.toString()
  return apiClient<DeviceList>(`/api/v1/devices${qs ? `?${qs}` : ''}`)
}

export async function renameDevice(mac: string, name: string): Promise<DeviceInventory> {
  return apiClient<DeviceInventory>(`/api/v1/devices/${mac}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  })
}

export async function forgetDevice(mac: string): Promise<void> {
  await apiClient(`/api/v1/devices/${mac}`, { method: 'DELETE' })
}

export async function wakeDevice(mac: string): Promise<{ status: string; broadcast: string }> {
  return apiClient<{ status: string; broadcast: string }>(`/api/v1/devices/${mac}/wake`, {
    method: 'POST',
  })
}
