-- Elementos visuales para la presentacion de nodos (starlark_scripts).
-- Usar Postgres 16 porque incluye pgcrypto
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS workflows (
  workflow_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT NOT NULL,
  description TEXT,
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  version     INT NOT NULL DEFAULT 1,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Mantener update xd
DROP TRIGGER IF EXISTS trg_workflows_updated_at ON workflows;
CREATE TRIGGER trg_workflows_updated_at
BEFORE UPDATE ON workflows
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS edges (
  edge_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workflow_id UUID NOT NULL REFERENCES workflows(workflow_id) ON DELETE CASCADE,

  from_node   TEXT NOT NULL REFERENCES starlark_scripts(sensor_id) ON DELETE RESTRICT,
  to_node     TEXT NOT NULL REFERENCES starlark_scripts(sensor_id) ON DELETE RESTRICT,

  edge_type   TEXT NOT NULL DEFAULT 'data',
  priority    INT  NOT NULL DEFAULT 0,

  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_edges_workflow ON edges(workflow_id);
CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_node);
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_node);
CREATE UNIQUE INDEX IF NOT EXISTS uq_edges_unique
  ON edges(workflow_id, from_node, to_node, edge_type);

CREATE TABLE IF NOT EXISTS node_layout (
  workflow_id UUID NOT NULL REFERENCES workflows(workflow_id) ON DELETE CASCADE,
  sensor_id   TEXT NOT NULL REFERENCES starlark_scripts(sensor_id) ON DELETE CASCADE,

  x DOUBLE PRECISION NOT NULL DEFAULT 0,
  y DOUBLE PRECISION NOT NULL DEFAULT 0,
  w DOUBLE PRECISION NOT NULL DEFAULT 240,
  h DOUBLE PRECISION NOT NULL DEFAULT 120,

  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, sensor_id)
);

DROP TRIGGER IF EXISTS trg_node_layout_updated_at ON node_layout;
CREATE TRIGGER trg_node_layout_updated_at
BEFORE UPDATE ON node_layout
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
