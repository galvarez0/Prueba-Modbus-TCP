# Dynamic actions (Starlark) for Prueba-Modbus-TCP
#
# Builtins:
#   mqtt_publish(topic=..., payload=...)
#   http_post_json(url=..., payload=...) -> status_code
#   pg_insert_sensor_telemetry(sensor_id=..., metric=..., value=..., tags=..., raw=...)
#   ch_insert_raw_event(source=..., topic=..., payload=...)
#
def on_event(event):
    kind = event.get("kind", "unknown")
    topic = event.get("topic", "")
    payload = event.get("payload", "")

    # Always store a raw copy in ClickHouse (if configured)
    ch_insert_raw_event(source=kind, topic=topic, payload=payload)

    if kind == "sensor":
        j = event.get("json", {})
        metric = j.get("metric", "")
        value = j.get("value", 0.0)
        sensor_id = j.get("sensor_id", "")

        # Example: alert on high temp
        if metric == "temperature_c" and value >= 80:
            mqtt_publish(topic="alerts/temperature", payload='{"sensor_id":"%s","value":%s}' % (sensor_id, value))

    if kind == "chirpstack_device":
        # Example: forward device events to a normalized topic
        mqtt_publish(topic="normalized/chirpstack_device", payload=payload)

    # For gateway events (protobuf), payload is raw bytes as string (may contain non-utf8),
    # but the decoder tries to put decoded json in event["gateway"].
    return None
