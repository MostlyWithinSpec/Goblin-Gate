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

  test('a 204 resolves to undefined and DELETE carries the CSRF header', async () => {
    document.cookie = 'gg_csrf=tok123'
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(apiClient('/api/v1/snapshots/a', { method: 'DELETE' })).resolves.toBeUndefined()
    expect(fetchMock.mock.calls[0][1].headers['X-CSRF-Token']).toBe('tok123')
    document.cookie = 'gg_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT'
  })
})
