CREATE TABLE IF NOT EXISTS starlark_scripts (
  sensor_id   text PRIMARY KEY,
  enabled     boolean NOT NULL DEFAULT true,
  script      text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO starlark_scripts (sensor_id, enabled, script)
VALUES (
  'default',
  true,
$$
# DEFAULT starlark script (sensor_id='default')
def on_event(event):
    kind = event.get("kind", "unknown")
    topic = event.get("topic", "")
    payload = event.get("payload", "")

    # Example: forward everything normalized
    mqtt_publish(topic="normalized/default", payload=payload)

    # Sensor alert example
    if kind == "sensor":
        j = event.get("json", {})
        metric = j.get("metric", "")
        value = j.get("value", 0.0)
        sid = j.get("sensor_id", "")
        if metric == "temperature_c" and value >= 80:
            mqtt_publish(topic="alerts/temperature", payload='{"sensor_id":"%s","value":%s}' % (sid, value))
    return None
$$
)
ON CONFLICT (sensor_id) DO NOTHING;

INSERT INTO starlark_scripts (sensor_id, enabled, script)
VALUES (
  'gw-1',
  true,
$$
# Custom script for sensor gw-1
def on_event(event):
    j = event.get("json", {})
    metric = j.get("metric", "")
    value = j.get("value", 0.0)

    # For gw-1, publish everything to a custom topic
    mqtt_publish(topic="custom/gw-1", payload=str(j))

    # Example: stricter threshold
    if metric == "temperature_c" and value >= 75:
        mqtt_publish(topic="alerts/gw-1/temp", payload='{"value":%s}' % value)
    return None
$$
)
ON CONFLICT (sensor_id) DO NOTHING;