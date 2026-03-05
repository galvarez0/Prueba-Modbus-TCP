import React, { useMemo, useState } from 'react'
import NodesPage from './pages/NodesPage'

type Route = 'nodes' // futuro: 'workflows' | 'layout' | 'settings'

function getRouteFromHash(): Route {
  const h = (typeof window !== 'undefined' ? window.location.hash : '').replace('#', '').trim()
  if (h === 'nodes' || h === '') return 'nodes'
  return 'nodes'
}

function setHash(route: Route) {
  if (typeof window === 'undefined') return
  window.location.hash = `#${route}`
}

class AppErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean; error?: string }
> {
  constructor(props: { children: React.ReactNode }) {
    super(props)
    this.state = { hasError: false }
  }

  static getDerivedStateFromError(err: unknown) {
    return { hasError: true, error: err instanceof Error ? err.message : String(err) }
  }

  componentDidCatch(err: unknown) {
    // útil si luego querés enviar logs a backend
    // eslint-disable-next-line no-console
    console.error('App crashed:', err)
  }

  render() {
    if (!this.state.hasError) return this.props.children

    return (
      <div style={{ padding: 24, maxWidth: 900, margin: '0 auto' }}>
        <h2 style={{ margin: 0 }}>La consola falló</h2>
        <p style={{ opacity: 0.8 }}>
          Ocurrió un error inesperado en la UI. Podés recargar la página o revisar la consola del navegador.
        </p>

        <pre
          style={{
            background: '#111',
            color: '#eee',
            padding: 12,
            borderRadius: 10,
            overflow: 'auto',
          }}
        >
          {this.state.error || 'Unknown error'}
        </pre>

        <div style={{ display: 'flex', gap: 10, marginTop: 12 }}>
          <button
            className="btn"
            onClick={() => window.location.reload()}
            style={{ cursor: 'pointer' }}
          >
            Recargar
          </button>
          <button
            className="btn secondary"
            onClick={() => this.setState({ hasError: false, error: undefined })}
            style={{ cursor: 'pointer' }}
          >
            Intentar continuar
          </button>
        </div>
      </div>
    )
  }
}

export default function App() {
  const [route, setRoute] = useState<Route>(() => getRouteFromHash())

  // Sync con hash (sin react-router)
  React.useEffect(() => {
    const onHash = () => setRoute(getRouteFromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  const envLabel = useMemo(() => {
    // Vite define import.meta.env.DEV/PROD; si no existe (build raro), caemos a string simple
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const meta = import.meta as any
    if (meta?.env?.DEV) return 'DEV'
    if (meta?.env?.PROD) return 'PROD'
    return 'ENV'
  }, [])

  return (
    <AppErrorBoundary>
      <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column' }}>
        {/* Top bar */}
        <div
          style={{
            position: 'sticky',
            top: 0,
            zIndex: 10,
            backdropFilter: 'blur(6px)',
            background: 'rgba(255,255,255,0.85)',
            borderBottom: '1px solid rgba(0,0,0,0.08)',
          }}
        >
          <div
            style={{
              maxWidth: 1200,
              margin: '0 auto',
              padding: '12px 16px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 12,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{ fontWeight: 800, letterSpacing: 0.2 }}>
                Prueba-Modbus Admin
              </div>

              <span
                style={{
                  fontSize: 12,
                  padding: '2px 8px',
                  borderRadius: 999,
                  border: '1px solid rgba(0,0,0,0.12)',
                  opacity: 0.9,
                }}
                title="Entorno"
              >
                {envLabel}
              </span>
            </div>

            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <button
                className={route === 'nodes' ? 'btn' : 'btn secondary'}
                onClick={() => {
                  setHash('nodes')
                  setRoute('nodes')
                }}
                title="Nodos Starlark"
              >
                Nodes
              </button>

              <a
                className="btn secondary"
                href="/api/nodes"
                target="_blank"
                rel="noreferrer"
                title="Abrir API de nodos (via proxy /api)"
                style={{ textDecoration: 'none', display: 'inline-flex', alignItems: 'center' }}
              >
                API
              </a>
            </div>
          </div>
        </div>

        {/* Main */}
        <div style={{ flex: 1 }}>
          {route === 'nodes' && <NodesPage />}
        </div>

        {/* Footer */}
        <div
          style={{
            borderTop: '1px solid rgba(0,0,0,0.08)',
            padding: '10px 16px',
            opacity: 0.75,
            fontSize: 12,
          }}
        >
          <div style={{ maxWidth: 1200, margin: '0 auto', display: 'flex', justifyContent: 'space-between', gap: 10 }}>
            <span>
              Consola admin (CRUD) para <code>telemetry.starlark_scripts</code>
            </span>
            <span>
              Ruta: <code>#{route}</code>
            </span>
          </div>
        </div>
      </div>
    </AppErrorBoundary>
  )
}