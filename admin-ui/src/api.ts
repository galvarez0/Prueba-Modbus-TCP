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

export type ListNodesResponse = { items: Node[] }

const API_BASE = import.meta.env.VITE_API_BASE || ''

const DEFAULT_TIMEOUT_MS = 12_000
const DEFAULT_RETRIES = 2

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms))
}

function isNetworkishError(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e)
  // "Failed to fetch" (browser), ECONNREFUSED (node), etc.
  return /failed to fetch|networkerror|econnrefused|etimedout|timeout/i.test(msg)
}

async function req<T>(
  path: string,
  init?: RequestInit & { timeoutMs?: number; retries?: number }
): Promise<T> {
  const timeoutMs = init?.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const retries = init?.retries ?? DEFAULT_RETRIES

  let lastErr: unknown = null

  for (let attempt = 0; attempt <= retries; attempt++) {
    const ac = new AbortController()
    const t = setTimeout(() => ac.abort(), timeoutMs)

    try {
      const res = await fetch(`${API_BASE}${path}`, {
        ...init,
        signal: ac.signal,
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          ...(init?.headers || {}),
        },
      })

      if (!res.ok) {
        const ct = res.headers.get('content-type') || ''
        let msg = `HTTP ${res.status}`

        try {
          if (ct.includes('application/json')) {
            const j = await res.json().catch(() => null)
            if (j && typeof j === 'object') msg = JSON.stringify(j)
          } else {
            const txt = await res.text().catch(() => '')
            if (txt) msg = txt
          }
        } catch {
          // ignore parsing errors
        }
        throw new Error(msg)
      }

      return (await res.json()) as T
    } catch (e) {
      lastErr = e

      const aborted = e instanceof DOMException && e.name === 'AbortError'
      const retryable = aborted || isNetworkishError(e)

      if (attempt < retries && retryable) {
        // backoff: 250ms, 500ms, 1000ms...
        await sleep(250 * Math.pow(2, attempt))
        continue
      }
      throw e
    } finally {
      clearTimeout(t)
    }
  }

  // should be unreachable
  throw lastErr ?? new Error('Unknown error')
}

export async function listNodes(params: { q?: string; enabled?: '' | 'true' | 'false' } = {}) {
  const usp = new URLSearchParams()
  if (params.q) usp.set('q', params.q)
  if (params.enabled) usp.set('enabled', params.enabled)
  const suffix = usp.toString() ? `?${usp.toString()}` : ''
  return req<ListNodesResponse>(`/api/nodes${suffix}`)
}

export async function createNode(n: {
  sensor_id: string
  type?: string
  name?: string | null
  description?: string | null
  tags?: string[]
  script: string
  enabled: boolean
}) {
  return req<{ ok: true }>(`/api/nodes`, {
    method: 'POST',
    body: JSON.stringify(n),
  })
}

export async function patchNode(
  sensorId: string,
  patch: Partial<{
    type: string
    name: string | null
    description: string | null
    tags: string[]
    script: string
    enabled: boolean
  }>
) {
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