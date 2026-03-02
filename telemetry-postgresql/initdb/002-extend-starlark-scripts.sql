-- Para editor de nodos

ALTER TABLE starlark_scripts
  ADD COLUMN IF NOT EXISTS type        TEXT NOT NULL DEFAULT 'custom',
  ADD COLUMN IF NOT EXISTS name        TEXT,
  ADD COLUMN IF NOT EXISTS description TEXT,
  ADD COLUMN IF NOT EXISTS tags        TEXT[];

CREATE INDEX IF NOT EXISTS idx_starlark_scripts_type ON starlark_scripts (type);
