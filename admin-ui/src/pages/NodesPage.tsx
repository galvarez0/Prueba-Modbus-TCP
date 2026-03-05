import React, { useEffect, useMemo, useRef, useState } from 'react'
import type { Node } from '../api'
import { createNode, deleteNode, listNodes, patchNode } from '../api'

type Mode = 'list' | 'create' | 'edit' | 'import'

type SortKey = 'updated_at' | 'sensor_id' | 'type' | 'enabled'
type SortDir = 'asc' | 'desc'

function fmtTs(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  if (typeof e === 'string') return e
  try {
    return JSON.stringify(e)
  } catch {
    return String(e)
  }
}

function clamp(n: number, min: number, max: number) {
  return Math.max(min, Math.min(max, n))
}

function downloadJson(filename: string, obj: unknown) {
  const blob = new Blob([JSON.stringify(obj, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

function uniqueSensorId(base: string, taken: Set<string>) {
  let s = base
  let i = 2
  while (taken.has(s)) {
    s = `${base}-${i}`
    i++
  }
  return s
}

function Toast({ msg, onClose }: { msg: string; onClose: () => void }) {
  useEffect(() => {
    const t = setTimeout(onClose, 2500)
    return () => clearTimeout(t)
  }, [onClose])

  return (
    <div
      style={{
        position: 'fixed',
        right: 16,
        bottom: 16,
        padding: '10px 12px',
        borderRadius: 10,
        background: 'rgba(0,0,0,0.78)',
        color: '#fff',
        zIndex: 9999,
        maxWidth: 420,
        fontSize: 14,
      }}
    >
      {msg}
    </div>
  )
}

export default function NodesPage() {
  const [items, setItems] = useState<Node[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)

  const [q, setQ] = useState('')
  const [enabledFilter, setEnabledFilter] = useState<'' | 'true' | 'false'>('')

  const [sortKey, setSortKey] = useState<SortKey>('updated_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  const [pageSize, setPageSize] = useState(20)
  const [page, setPage] = useState(1)

  const [autoRefreshSec, setAutoRefreshSec] = useState<number>(0) // 0=off
  const autoRefreshTimerRef = useRef<number | null>(null)

  const [mode, setMode] = useState<Mode>('list')
  const [editing, setEditing] = useState<Node | null>(null)

  const [selected, setSelected] = useState<Set<string>>(new Set())

  async function refresh() {
    setLoading(true)
    setErr(null)
    try {
      const res = await listNodes({ q: q.trim() || undefined, enabled: enabledFilter })
      setItems(res.items)
      // reconciliar selección si alguien borró algo
      setSelected((prev) => {
        const live = new Set(res.items.map((x) => x.sensor_id))
        const next = new Set<string>()
        for (const id of prev) if (live.has(id)) next.add(id)
        return next
      })
    } catch (e: unknown) {
      setErr(errMsg(e) || 'Error desconocido')
    } finally {
      setLoading(false)
    }
  }

  // initial load
  useEffect(() => {
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // debounce search/filter
  useEffect(() => {
    const t = window.setTimeout(() => {
      setPage(1)
      refresh()
    }, 250)
    return () => window.clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, enabledFilter])

  // auto refresh
  useEffect(() => {
    if (autoRefreshTimerRef.current) {
      window.clearInterval(autoRefreshTimerRef.current)
      autoRefreshTimerRef.current = null
    }
    if (autoRefreshSec > 0) {
      autoRefreshTimerRef.current = window.setInterval(() => {
        refresh()
      }, autoRefreshSec * 1000)
    }
    return () => {
      if (autoRefreshTimerRef.current) window.clearInterval(autoRefreshTimerRef.current)
      autoRefreshTimerRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoRefreshSec])

  const takenIds = useMemo(() => new Set(items.map((x) => x.sensor_id)), [items])

  const sorted = useMemo(() => {
    const arr = [...items]
    arr.sort((a, b) => {
      const dir = sortDir === 'asc' ? 1 : -1
      if (sortKey === 'updated_at') {
        return (new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime()) * dir
      }
      if (sortKey === 'enabled') {
        return ((a.enabled === b.enabled ? 0 : a.enabled ? 1 : -1) as number) * dir
      }
      // string keys
      const av = String((a as any)[sortKey] ?? '')
      const bv = String((b as any)[sortKey] ?? '')
      return av.localeCompare(bv) * dir
    })
    return arr
  }, [items, sortKey, sortDir])

  const total = sorted.length
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const safePage = clamp(page, 1, totalPages)
  const pageItems = useMemo(() => {
    const start = (safePage - 1) * pageSize
    return sorted.slice(start, start + pageSize)
  }, [sorted, safePage, pageSize])

  const selectedCount = selected.size
  const enabledCount = items.filter((x) => x.enabled).length
  const disabledCount = items.length - enabledCount

  const header = useMemo(() => {
    return (
      <div className="header">
        <div>
          <div className="title">Starlark Nodes</div>
          <div className="sub">Cada fila = un nodo ejecutable (telemetry.starlark_scripts)</div>
          <div className="sub">
            Total: <b>{items.length}</b> · Enabled: <b>{enabledCount}</b> · Disabled: <b>{disabledCount}</b>
          </div>
        </div>
        <div className="actions">
          <button
            className="btn"
            onClick={() => {
              setMode('create')
              setEditing(null)
            }}
          >
            + Nuevo nodo
          </button>
          <button className="btn secondary" onClick={() => setMode('import')} title="Importar nodos desde JSON">
            Importar
          </button>
          <button
            className="btn secondary"
            onClick={() => downloadJson(`nodes-export-${new Date().toISOString()}.json`, { items })}
            title="Exportar nodos como JSON"
          >
            Exportar
          </button>
          <button className="btn" onClick={refresh} disabled={loading} title="Refrescar lista">
            ↻ Refrescar
          </button>
        </div>
      </div>
    )
  }, [loading, items, enabledCount, disabledCount])

  async function bulkSetEnabled(next: boolean) {
    const ids = [...selected]
    if (ids.length === 0) return
    setLoading(true)
    setErr(null)
    try {
      for (const id of ids) {
        // no bloquear default aquí; sí permitimos enable/disable default si quisieras (si no, lo bloqueamos también)
        await patchNode(id, { enabled: next })
      }
      setToast(`Actualizado: ${ids.length} nodo(s)`)
      await refresh()
    } catch (e: unknown) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
    }
  }

  async function duplicateNode(n: Node) {
    const base = `${n.sensor_id}-copy`
    const newId = uniqueSensorId(base, takenIds)
    setLoading(true)
    setErr(null)
    try {
      await createNode({
        sensor_id: newId,
        type: n.type || 'custom',
        name: (n.name ? `${n.name} (copy)` : null) as any,
        description: n.description ?? null,
        tags: n.tags ?? [],
        enabled: false,
        script: n.script,
      })
      setToast(`Duplicado: ${n.sensor_id} → ${newId} (disabled)`)
      await refresh()
    } catch (e: unknown) {
      setErr(errMsg(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="page">
      {toast && <Toast msg={toast} onClose={() => setToast(null)} />}

      {header}

      <div className="toolbar" style={{ gap: 10, flexWrap: 'wrap' }}>
        <input
          className="input"
          placeholder="Buscar por sensor_id, name, type…"
          value={q}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => setQ(e.target.value)}
          style={{ minWidth: 260 }}
        />

        <select
          className="select"
          value={enabledFilter}
          onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setEnabledFilter(e.target.value as '' | 'true' | 'false')}
          title="Filtrar por enabled"
        >
          <option value="">Todos</option>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>

        <select
          className="select"
          value={`${sortKey}:${sortDir}`}
          onChange={(e: React.ChangeEvent<HTMLSelectElement>) => {
            const [k, d] = e.target.value.split(':') as [SortKey, SortDir]
            setSortKey(k)
            setSortDir(d)
          }}
          title="Orden"
        >
          <option value="updated_at:desc">updated_at ↓</option>
          <option value="updated_at:asc">updated_at ↑</option>
          <option value="sensor_id:asc">sensor_id A→Z</option>
          <option value="sensor_id:desc">sensor_id Z→A</option>
          <option value="type:asc">type A→Z</option>
          <option value="type:desc">type Z→A</option>
          <option value="enabled:desc">enabled (true primero)</option>
          <option value="enabled:asc">enabled (false primero)</option>
        </select>

        <select
          className="select"
          value={String(pageSize)}
          onChange={(e: React.ChangeEvent<HTMLSelectElement>) => {
            setPageSize(Number(e.target.value))
            setPage(1)
          }}
          title="Tamaño de página"
        >
          <option value="10">10/pg</option>
          <option value="20">20/pg</option>
          <option value="50">50/pg</option>
          <option value="100">100/pg</option>
        </select>

        <select
          className="select"
          value={String(autoRefreshSec)}
          onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setAutoRefreshSec(Number(e.target.value))}
          title="Auto refresh"
        >
          <option value="0">Auto-refresh: Off</option>
          <option value="2">Auto-refresh: 2s</option>
          <option value="5">Auto-refresh: 5s</option>
          <option value="10">Auto-refresh: 10s</option>
          <option value="30">Auto-refresh: 30s</option>
        </select>

        <div className="meta" style={{ marginLeft: 'auto' }}>
          {loading ? '⏳ Cargando…' : `${total} nodo(s) · pág ${safePage}/${totalPages}`}
        </div>
      </div>

      {err && (
        <div className="error" style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span>⚠ {err}</span>
          <button className="btn secondary" onClick={refresh} disabled={loading}>
            Reintentar
          </button>
        </div>
      )}

      {selectedCount > 0 && (
        <div
          className="card"
          style={{
            padding: 12,
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            justifyContent: 'space-between',
          }}
        >
          <div>
            Seleccionados: <b>{selectedCount}</b>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn secondary" onClick={() => bulkSetEnabled(true)} disabled={loading}>
              Enable seleccionados
            </button>
            <button className="btn secondary" onClick={() => bulkSetEnabled(false)} disabled={loading}>
              Disable seleccionados
            </button>
            <button
              className="btn secondary"
              onClick={() => setSelected(new Set())}
              disabled={loading}
              title="Limpiar selección"
            >
              Limpiar
            </button>
          </div>
        </div>
      )}

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th style={{ width: 40 }}>
                <input
                  type="checkbox"
                  checked={pageItems.length > 0 && pageItems.every((n) => selected.has(n.sensor_id))}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                    const next = new Set(selected)
                    if (e.target.checked) {
                      for (const n of pageItems) next.add(n.sensor_id)
                    } else {
                      for (const n of pageItems) next.delete(n.sensor_id)
                    }
                    setSelected(next)
                  }}
                  title="Seleccionar todos en la página"
                />
              </th>
              <th>sensor_id</th>
              <th>type</th>
              <th>enabled</th>
              <th>updated_at</th>
              <th style={{ width: 320 }}></th>
            </tr>
          </thead>
          <tbody>
            {pageItems.map((n) => (
              <tr key={n.sensor_id}>
                <td>
                  <input
                    type="checkbox"
                    checked={selected.has(n.sensor_id)}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                      const next = new Set(selected)
                      if (e.target.checked) next.add(n.sensor_id)
                      else next.delete(n.sensor_id)
                      setSelected(next)
                    }}
                  />
                </td>
                <td className="mono" title={n.name || undefined}>
                  {n.sensor_id}
                </td>
                <td>{n.type}</td>
                <td>
                  <label className="switch">
                    <input
                      type="checkbox"
                      checked={n.enabled}
                      onChange={async (e: React.ChangeEvent<HTMLInputElement>) => {
                        const next = e.target.checked
                        setErr(null)

                        // optimistic UI
                        setItems((prev) =>
                          prev.map((x) => (x.sensor_id === n.sensor_id ? { ...x, enabled: next } : x))
                        )

                        try {
                          await patchNode(n.sensor_id, { enabled: next })
                          setToast(`Actualizado: ${n.sensor_id} → enabled=${next}`)
                          await refresh()
                        } catch (e: unknown) {
                          // rollback (re-fetch)
                          setErr(errMsg(e))
                          await refresh()
                        }
                      }}
                    />
                    <span className="slider" />
                  </label>
                </td>
                <td>{fmtTs(n.updated_at)}</td>
                <td className="rowActions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <button
                    className="btn secondary"
                    onClick={() => {
                      setEditing(n)
                      setMode('edit')
                    }}
                  >
                    Editar
                  </button>

                  <button className="btn secondary" onClick={() => duplicateNode(n)} title="Clonar este nodo">
                    Duplicar
                  </button>

                  <button
                    className="btn danger"
                    disabled={n.sensor_id === 'default'}
                    onClick={async () => {
                      if (!confirm(`Eliminar nodo ${n.sensor_id}?`)) return
                      setErr(null)
                      setLoading(true)
                      try {
                        await deleteNode(n.sensor_id)
                        setToast(`Eliminado: ${n.sensor_id}`)
                        await refresh()
                      } catch (e: unknown) {
                        setErr(errMsg(e))
                      } finally {
                        setLoading(false)
                      }
                    }}
                  >
                    Eliminar
                  </button>
                </td>
              </tr>
            ))}

            {pageItems.length === 0 && (
              <tr>
                <td colSpan={6} className="empty">
                  No hay nodos.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="toolbar" style={{ justifyContent: 'space-between' }}>
        <div className="meta">
          Página: <b>{safePage}</b> / <b>{totalPages}</b>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn secondary" disabled={safePage <= 1} onClick={() => setPage(1)}>
            ⏮
          </button>
          <button className="btn secondary" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>
            ◀
          </button>
          <button className="btn secondary" disabled={safePage >= totalPages} onClick={() => setPage(safePage + 1)}>
            ▶
          </button>
          <button className="btn secondary" disabled={safePage >= totalPages} onClick={() => setPage(totalPages)}>
            ⏭
          </button>
        </div>
      </div>

      {(mode === 'create' || mode === 'edit') && (
        <NodeEditor
          mode={mode === 'create' ? 'create' : 'edit'}
          initial={editing}
          onClose={() => {
            setMode('list')
            setEditing(null)
          }}
          onSaved={async () => {
            setMode('list')
            setEditing(null)
            setToast('Guardado ✅')
            await refresh()
          }}
        />
      )}

      {mode === 'import' && (
        <ImportModal
          onClose={() => setMode('list')}
          onImport={async (nodes) => {
            // Importa como createNode; si existe, fallará. (Lo hacemos explícito y seguro)
            setLoading(true)
            setErr(null)
            try {
              let ok = 0
              for (const n of nodes) {
                await createNode({
                  sensor_id: n.sensor_id,
                  type: n.type || 'custom',
                  name: n.name ?? null,
                  description: n.description ?? null,
                  tags: n.tags ?? [],
                  enabled: n.enabled ?? true,
                  script: n.script ?? '',
                })
                ok++
              }
              setToast(`Importados: ${ok} nodo(s)`)
              setMode('list')
              await refresh()
            } catch (e: unknown) {
              setErr(errMsg(e))
            } finally {
              setLoading(false)
            }
          }}
        />
      )}
    </div>
  )
}

function NodeEditor(props: {
  mode: 'create' | 'edit'
  initial: Node | null
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const isEdit = props.mode === 'edit'

  const [sensorId, setSensorId] = useState(props.initial?.sensor_id || '')
  const [type, setType] = useState(props.initial?.type || 'custom')
  const [name, setName] = useState(props.initial?.name || '')
  const [description, setDescription] = useState(props.initial?.description || '')
  const [tags, setTags] = useState((props.initial?.tags || []).join(','))
  const [enabled, setEnabled] = useState(props.initial?.enabled ?? true)
  const [script, setScript] = useState(
    props.initial?.script ||
      `# node script\n\n# expected entry point\ndef on_event(event):\n    return None\n`
  )

  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  return (
    <div className="modalOverlay" role="dialog" aria-modal="true">
      <div className="modal">
        <div className="modalHeader">
          <div>
            <div className="title">{isEdit ? `Editar nodo: ${props.initial?.sensor_id}` : 'Nuevo nodo'}</div>
            <div className="sub">Guarda en telemetry.starlark_scripts y actualiza updated_at para hot-reload.</div>
          </div>
          <button className="btn secondary" onClick={props.onClose} disabled={saving}>
            Cerrar
          </button>
        </div>

        {err && (
          <div className="error" style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
            <span>⚠ {err}</span>
          </div>
        )}

        <div className="grid">
          <div>
            <label className="label">sensor_id</label>
            <input
              className="input mono"
              value={sensorId}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setSensorId(e.target.value)}
              disabled={isEdit || saving}
            />
          </div>

          <div>
            <label className="label">type</label>
            <input
              className="input"
              value={type}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setType(e.target.value)}
              disabled={saving}
            />
          </div>

          <div>
            <label className="label">name</label>
            <input
              className="input"
              value={name}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setName(e.target.value)}
              disabled={saving}
            />
          </div>

          <div>
            <label className="label">enabled</label>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', height: 40 }}>
              <label className="switch">
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEnabled(e.target.checked)}
                  disabled={saving}
                />
                <span className="slider" />
              </label>
              <span className="sub">{enabled ? 'activo' : 'inactivo'}</span>
            </div>
          </div>

          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">description</label>
            <input
              className="input"
              value={description}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setDescription(e.target.value)}
              disabled={saving}
            />
          </div>

          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">tags (coma-separado)</label>
            <input
              className="input"
              value={tags}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setTags(e.target.value)}
              disabled={saving}
            />
          </div>

          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">script (Starlark)</label>
            <textarea
              className="textarea mono"
              value={script}
              onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setScript(e.target.value)}
              disabled={saving}
              style={{ minHeight: 260 }}
            />
          </div>
        </div>

        <div className="modalFooter">
          <button
            className="btn"
            disabled={saving}
            onClick={async () => {
              setSaving(true)
              setErr(null)
              try {
                const tagList = tags
                  .split(',')
                  .map((t) => t.trim())
                  .filter(Boolean)

                if (!sensorId.trim()) throw new Error('sensor_id requerido')
                if (!script.trim()) throw new Error('script requerido')
                if (sensorId.includes(' ')) throw new Error('sensor_id no puede contener espacios')

                if (isEdit) {
                  await patchNode(sensorId.trim(), {
                    type: type.trim() || 'custom',
                    name: name || null,
                    description: description || null,
                    tags: tagList,
                    enabled,
                    script,
                  })
                } else {
                  await createNode({
                    sensor_id: sensorId.trim(),
                    type: type.trim() || 'custom',
                    name: name || null,
                    description: description || null,
                    tags: tagList,
                    enabled,
                    script,
                  })
                }
                await props.onSaved()
              } catch (e: unknown) {
                setErr(errMsg(e))
              } finally {
                setSaving(false)
              }
            }}
          >
            Guardar
          </button>
        </div>
      </div>
    </div>
  )
}

function ImportModal(props: {
  onClose: () => void
  onImport: (nodes: Array<Partial<Node> & { sensor_id: string; script: string }>) => Promise<void>
}) {
  const [txt, setTxt] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  return (
    <div className="modalOverlay" role="dialog" aria-modal="true">
      <div className="modal">
        <div className="modalHeader">
          <div>
            <div className="title">Importar nodos</div>
            <div className="sub">Pegá un JSON con formato {"{ items: [...] }"} o directamente un array.</div>
          </div>
          <button className="btn secondary" onClick={props.onClose} disabled={busy}>
            Cerrar
          </button>
        </div>

        {err && <div className="error">⚠ {err}</div>}

        <div style={{ padding: 12 }}>
          <textarea
            className="textarea mono"
            value={txt}
            onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setTxt(e.target.value)}
            placeholder='Ejemplo: {"items":[{"sensor_id":"x","script":"def on_event(event):\n  return None\n","enabled":true}]}'
            style={{ minHeight: 260 }}
            disabled={busy}
          />
        </div>

        <div className="modalFooter" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button
            className="btn"
            disabled={busy}
            onClick={async () => {
              setErr(null)
              setBusy(true)
              try {
                const parsed = JSON.parse(txt)
                const items = Array.isArray(parsed) ? parsed : parsed?.items
                if (!Array.isArray(items)) throw new Error('JSON inválido: esperaba array o {items: array}')

                // Validación mínima
                const nodes = items.map((x: any) => {
                  if (!x?.sensor_id) throw new Error('Falta sensor_id en uno de los items')
                  if (!x?.script) throw new Error(`Falta script en ${x.sensor_id}`)
                  return x as Partial<Node> & { sensor_id: string; script: string }
                })

                await props.onImport(nodes)
              } catch (e: unknown) {
                setErr(errMsg(e))
              } finally {
                setBusy(false)
              }
            }}
          >
            Importar
          </button>
        </div>
      </div>
    </div>
  )
}