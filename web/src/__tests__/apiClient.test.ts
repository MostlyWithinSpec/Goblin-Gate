import { afterEach, describe, expect, test, vi } from 'vitest'
import { apiClient } from '../api/client'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiClient', () => {
  test('throws the server error message with details', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'invalid_patch', message: 'could not apply the change', details: 'unknown field' } }), { status: 400 })))
    await expect(apiClient('/api/v1/config/candidate')).rejects.toThrow('could not apply the change: unknown field')
  })

  test('falls back to the status for non-JSON errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('boom', { status: 502, statusText: 'Bad Gateway' })))
    await expect(apiClient('/x')).rejects.toThrow('Bad Gateway')
  })
})
