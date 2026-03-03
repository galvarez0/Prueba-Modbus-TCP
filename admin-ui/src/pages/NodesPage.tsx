import { useEffect, useMemo, useState } from 'react'
import type { Node } from '../api'
import { createNode, deleteNode, listNodes, patchNode } from '../api'

type Mode = 'list' | 'create' | 'edit'

function fmtTs(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

export default function NodesPage() {
  const [items, setItems] = useState<Node[]>([])
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const [q, setQ] = useState('')
  const [enabledFilter, setEnabledFilter] = useState<'' | 'true' | 'false'>('')

  const [mode, setMode] = useState<Mode>('list')
  const [editing, setEditing] = useState<Node | null>(null)

  const filteredCount = items.length

  async function refresh() {
    setLoading(true)
    setErr(null)
    try {
      const res = await listNodes({ q: q.trim() || undefined, enabled: enabledFilter })
      setItems(res.items)
    } catch (e: any) {
      setErr(e?.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const t = setTimeout(() => refresh(), 250)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, enabledFilter])

  const header = useMemo(() => {
    return (
      <div className="header">
        <div>
          <div className="title">Starlark Nodes</div>
          <div className="sub">Cada fila = un nodo ejecutable (telemetry.starlark_scripts)</div>
        </div>
        <div className="actions">
          <button className="btn" onClick={() => { setMode('create'); setEditing(null) }}>
            + Nuevo nodo
          </button>
          <button className="btn" onClick={refresh} disabled={loading}>
            ↻ Refrescar
          </button>
        </div>
      </div>
    )
  }, [loading])

  return (
    <div className="page">
      {header}

      <div className="toolbar">
        <input
          className="input"
          placeholder="Buscar por sensor_id, name, type…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <select className="select" value={enabledFilter} onChange={(e) => setEnabledFilter(e.target.value as any)}>
          <option value="">Todos</option>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>
        <div className="meta">
          {loading ? 'Cargando…' : `${filteredCount} nodo(s)`}
        </div>
      </div>

      {err && <div className="error">{err}</div>}

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th>sensor_id</th>
              <th>type</th>
              <th>enabled</th>
              <th>updated_at</th>
              <th style={{ width: 220 }}></th>
            </tr>
          </thead>
          <tbody>
            {items.map((n) => (
              <tr key={n.sensor_id}>
                <td className="mono">{n.sensor_id}</td>
                <td>{n.type}</td>
                <td>
                  <label className="switch">
                    <input
                      type="checkbox"
                      checked={n.enabled}
                      onChange={async (e) => {
                        const next = e.target.checked
                        try {
                          await patchNode(n.sensor_id, { enabled: next })
                          await refresh()
                        } catch (e: any) {
                          setErr(e?.message || String(e))
                        }
                      }}
                    />
                    <span className="slider" />
                  </label>
                </td>
                <td>{fmtTs(n.updated_at)}</td>
                <td className="rowActions">
                  <button
                    className="btn secondary"
                    onClick={() => {
                      setEditing(n)
                      setMode('edit')
                    }}
                  >
                    Editar
                  </button>
                  <button
                    className="btn danger"
                    disabled={n.sensor_id === 'default'}
                    onClick={async () => {
                      if (!confirm(`Eliminar nodo ${n.sensor_id}?`)) return
                      try {
                        await deleteNode(n.sensor_id)
                        await refresh()
                      } catch (e: any) {
                        setErr(e?.message || String(e))
                      }
                    }}
                  >
                    Eliminar
                  </button>
                </td>
              </tr>
            ))}
            {items.length === 0 && (
              <tr>
                <td colSpan={5} className="empty">
                  No hay nodos.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {(mode === 'create' || mode === 'edit') && (
        <NodeEditor
          mode={mode}
          initial={editing}
          onClose={() => {
            setMode('list')
            setEditing(null)
          }}
          onSaved={async () => {
            setMode('list')
            setEditing(null)
            await refresh()
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
  const [script, setScript] = useState(props.initial?.script || `# node script\n\n# expected entry point\ndef on_event(event):\n    return None\n`)

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

        {err && <div className="error">{err}</div>}

        <div className="grid">
          <div>
            <label className="label">sensor_id</label>
            <input className="input mono" value={sensorId} onChange={(e) => setSensorId(e.target.value)} disabled={isEdit || saving} />
          </div>
          <div>
            <label className="label">type</label>
            <input className="input" value={type} onChange={(e) => setType(e.target.value)} disabled={saving} />
          </div>
          <div>
            <label className="label">name</label>
            <input className="input" value={name} onChange={(e) => setName(e.target.value)} disabled={saving} />
          </div>
          <div>
            <label className="label">enabled</label>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', height: 40 }}>
              <label className="switch">
                <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} disabled={saving} />
                <span className="slider" />
              </label>
              <span className="sub">{enabled ? 'activo' : 'inactivo'}</span>
            </div>
          </div>
          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">description</label>
            <input className="input" value={description} onChange={(e) => setDescription(e.target.value)} disabled={saving} />
          </div>
          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">tags (coma-separado)</label>
            <input className="input" value={tags} onChange={(e) => setTags(e.target.value)} disabled={saving} />
          </div>
          <div style={{ gridColumn: '1 / -1' }}>
            <label className="label">script (Starlark)</label>
            <textarea className="textarea mono" value={script} onChange={(e) => setScript(e.target.value)} disabled={saving} />
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
              } catch (e: any) {
                setErr(e?.message || String(e))
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
