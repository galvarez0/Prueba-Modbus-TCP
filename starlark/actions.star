# Dynamic actions (Starlark) for Prueba-Modbus-TCP
#
# Builtins:
#   mqtt_publish(topic=..., payload=...)
#   http_post_json(url=..., payload=...) -> status_code
#   pg_insert_sensor_telemetry(sensor_id=..., metric=..., value=..., tags=..., raw=...)
#   ch_insert_raw_event(source=..., topic=..., payload=...)
#
# Event contract:
#   event = {
#     "ts": "...",
#     "kind": "sensor" | "chirpstack_device" | "chirpstack_gateway" | "unknown",
#     "topic": "...",
#     "payload": "...",
#     "json": {...}      # when JSON parse succeeds
#     "gateway": {...}   # when gateway protobuf decode succeeds
#   }
#
def on_event(event):
    kind = event.get("kind", "unknown")
    topic = event.get("topic", "")
    payload = event.get("payload", "")

    # Always store raw (ClickHouse optional)
    ch_insert_raw_event(source=kind, topic=topic, payload=payload)

    if kind == "sensor":
        j = event.get("json", {})
        metric = j.get("metric", "")
        value = j.get("value", 0.0)
        sensor_id = j.get("sensor_id", "")

        # Optional: also store to Postgres via builtin (no-op if PG not configured)
        pg_insert_sensor_telemetry(
            sensor_id=sensor_id,
            metric=metric,
            value=value,
            tags='{"via":"starlark"}',
            raw=str(j),
        )

        if metric == "temperature_c" and value >= 80:
            mqtt_publish(topic="alerts/temperature",
                         payload='{"sensor_id":"%s","value":%s}' % (sensor_id, value))

    if kind == "chirpstack_device":
        mqtt_publish(topic="normalized/chirpstack_device", payload=payload)

    if kind == "chirpstack_gateway":
        gw = event.get("gateway", {})
        mqtt_publish(topic="normalized/chirpstack_gateway",
                     payload='{"region":"%s","gateway_id":"%s","event_type":"%s"}' % (
                         gw.get("region",""), gw.get("gateway_id",""), gw.get("event_type","")
                     ))
    return None