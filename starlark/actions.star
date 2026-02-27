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
    mqtt_publish(topic='normalized/file_default', payload=event.get('payload',''))
    return None
