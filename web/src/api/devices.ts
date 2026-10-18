// Device inventory API — live state, not candidate config.
import { apiClient } from './client'

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
}

export async function listDevices(interfaceFilter?: string): Promise<DeviceInventory[]> {
  const params = new URLSearchParams()
  if (interfaceFilter) params.set('interface', interfaceFilter)
  const qs = params.toString()
  const res = await apiClient<{ devices: DeviceInventory[] }>(`/api/v1/devices${qs ? `?${qs}` : ''}`)
  return res.devices
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
