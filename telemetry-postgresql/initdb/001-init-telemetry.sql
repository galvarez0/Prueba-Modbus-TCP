-- Telemetry Postgres init for Prueba-Modbus-TCP
-- Separate DB from ChirpStack Postgres (per requirement).

CREATE TABLE IF NOT EXISTS sensor_telemetry (
  ts           TIMESTAMPTZ NOT NULL DEFAULT now(),
  sensor_id    TEXT        NOT NULL,
  metric       TEXT        NOT NULL,
  value        DOUBLE PRECISION NOT NULL,
  tags         JSONB       NOT NULL DEFAULT '{}'::jsonb,
  raw          JSONB       NOT NULL
);

CREATE INDEX IF NOT EXISTS sensor_telemetry_ts_idx ON sensor_telemetry (ts);
CREATE INDEX IF NOT EXISTS sensor_telemetry_sensor_idx ON sensor_telemetry (sensor_id);

-- Optional: NOTIFY channel so consumers can LISTEN telemetry_events.
CREATE OR REPLACE FUNCTION notify_telemetry_insert() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('telemetry_events', row_to_json(NEW)::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_notify_telemetry_insert ON sensor_telemetry;
CREATE TRIGGER trg_notify_telemetry_insert
  AFTER INSERT ON sensor_telemetry
  FOR EACH ROW EXECUTE FUNCTION notify_telemetry_insert();
