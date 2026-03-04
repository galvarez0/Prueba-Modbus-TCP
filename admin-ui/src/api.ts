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

const API_BASE = import.meta.env.VITE_API_BASE || ''

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers || {}),
    },
  })
  if (!res.ok) {
    const txt = await res.text().catch(() => '')
    throw new Error(txt || `HTTP ${res.status}`)
  }
  return (await res.json()) as T
}

export async function listNodes(params: { q?: string; enabled?: '' | 'true' | 'false' } = {}) {
  const usp = new URLSearchParams()
  if (params.q) usp.set('q', params.q)
  if (params.enabled) usp.set('enabled', params.enabled)
  const suffix = usp.toString() ? `?${usp.toString()}` : ''
  return req<{ items: Node[] }>(`/api/nodes${suffix}`)
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

export async function patchNode(sensorId: string, patch: Partial<{ type: string; name: string | null; description: string | null; tags: string[]; script: string; enabled: boolean }>) {
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
