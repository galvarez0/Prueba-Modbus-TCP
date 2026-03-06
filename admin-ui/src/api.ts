export type Node = {
  sensor_id: string
  type: string
  name?: string | null
  description?: string | null
  tags?: string[]
  script: string
  enabled: boolean
  updated_at: string
}

export type ListNodesResponse = {
  items: Node[]
}

export type CreateNodeInput = {
  sensor_id: string
  type?: string
  name?: string | null
  description?: string | null
  tags?: string[]
  script: string
  enabled: boolean
}

export type PatchNodeInput = Partial<{
  type: string
  name: string | null
  description: string | null
  tags: string[]
  script: string
  enabled: boolean
}>

const API_BASE = import.meta.env.VITE_API_BASE || ''

const DEFAULT_TIMEOUT_MS = 12_000
const DEFAULT_RETRIES = 2

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function isNetworkishError(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e)
  return /failed to fetch|networkerror|econnrefused|etimedout|timeout/i.test(msg)
}

function extractErrorMessage(data: unknown, fallback: string): string {
  if (!data || typeof data !== 'object') return fallback

  const obj = data as Record<string, unknown>

  if (typeof obj.error === 'string' && obj.error.trim()) return obj.error
  if (typeof obj.message === 'string' && obj.message.trim()) return obj.message
  if (typeof obj.detail === 'string' && obj.detail.trim()) return obj.detail

  try {
    return JSON.stringify(obj)
  } catch {
    return fallback
  }
}

async function req<T>(
  path: string,
  init?: RequestInit & { timeoutMs?: number; retries?: number }
): Promise<T> {
  const timeoutMs = init?.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const method = (init?.method || 'GET').toUpperCase()

  // Por seguridad, reintentar por defecto solo GET
  const retries = init?.retries ?? (method === 'GET' ? DEFAULT_RETRIES : 0)

  let lastErr: unknown = null

  for (let attempt = 0; attempt <= retries; attempt++) {
    const ac = new AbortController()
    const timer = setTimeout(() => ac.abort(), timeoutMs)

    try {
      const hasJsonBody = typeof init?.body === 'string' && init.body.length > 0

      const res = await fetch(`${API_BASE}${path}`, {
        ...init,
        signal: ac.signal,
        headers: {
          Accept: 'application/json',
          ...(hasJsonBody ? { 'Content-Type': 'application/json' } : {}),
          ...(init?.headers || {}),
        },
      })

      if (!res.ok) {
        const contentType = res.headers.get('content-type') || ''
        let msg = `HTTP ${res.status}`

        try {
          if (contentType.includes('application/json')) {
            const json = await res.json().catch(() => null)
            msg = extractErrorMessage(json, msg)
          } else {
            const text = await res.text().catch(() => '')
            if (text) msg = text
          }
        } catch {
          // dejamos el fallback
        }

        throw new Error(msg)
      }

      // Soporta respuestas vacías / 204
      if (res.status === 204) {
        return undefined as T
      }

      const text = await res.text()
      if (!text) {
        return undefined as T
      }

      return JSON.parse(text) as T
    } catch (e: unknown) {
      lastErr = e

      const aborted = e instanceof DOMException && e.name === 'AbortError'
      const retryable = aborted || isNetworkishError(e)

      if (attempt < retries && retryable) {
        await sleep(250 * Math.pow(2, attempt))
        continue
      }

      if (aborted) {
        throw new Error(`Request timeout after ${timeoutMs}ms`)
      }

      throw e
    } finally {
      clearTimeout(timer)
    }
  }

  throw lastErr ?? new Error('Unknown error')
}

export async function listNodes(params: { q?: string; enabled?: '' | 'true' | 'false' } = {}) {
  const usp = new URLSearchParams()
  if (params.q) usp.set('q', params.q)
  if (params.enabled) usp.set('enabled', params.enabled)
  const suffix = usp.toString() ? `?${usp.toString()}` : ''

  return req<ListNodesResponse>(`/api/nodes${suffix}`, {
    method: 'GET',
  })
}

export async function createNode(input: CreateNodeInput) {
  return req<{ ok: true }>(`/api/nodes`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function patchNode(sensorId: string, patch: PatchNodeInput) {
  return req<{ ok: true }>(`/api/nodes/${encodeURIComponent(sensorId)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  })
}

export async function deleteNode(sensorId: string) {
  return req<{ ok: true }>(`/api/nodes/${encodeURIComponent(sensorId)}`, {
    method: 'DELETE',
  })
}