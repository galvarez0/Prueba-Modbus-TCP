import json
import os
import sys
import time
import requests
#Si lanza error el paho.mqtt darle en terminal 'pip install paho-mqtt requests'
import paho.mqtt.client as mqtt

MQTT_BROKER = os.getenv("MQTT_BROKER", "tcp://mosquitto:1883")
MQTT_SUB_TOPIC = os.getenv("MQTT_SUB_TOPIC", "application/+/device/+/event/up")
MODBUS_WRITE_URL = os.getenv("MODBUS_WRITE_URL", "").strip()

def parse_mqtt_url(url: str):
    if "://" in url:
        scheme, rest = url.split("://", 1)
    else:
        scheme, rest = "tcp", url
    if ":" in rest:
        host, port_s = rest.split(":", 1)
        port = int(port_s)
    else:
        host, port = rest, 1883
    return scheme, host, port

def log(msg: str):
    print(msg, flush=True)

def on_connect(client, userdata, flags, rc):
    log(f"[bridge] MQTT connected rc={rc}, subscribing: {MQTT_SUB_TOPIC}")
    client.subscribe(MQTT_SUB_TOPIC)

def on_message(client, userdata, msg):
    payload = msg.payload.decode("utf-8", errors="replace")
    log(f"[bridge] MQTT topic={msg.topic} payload={payload}")
    try:
        data = json.loads(payload)
    except Exception as e:
        log(f"[bridge] WARN: could not json-decode payload: {e}")
        return
    if not MODBUS_WRITE_URL:
        return
    try:
        r = requests.post(MODBUS_WRITE_URL, json=data, timeout=5)
        log(f"[bridge] POST {MODBUS_WRITE_URL} -> {r.status_code} {r.text[:200]}")
    except Exception as e:
        log(f"[bridge] ERROR calling MODBUS_WRITE_URL: {e}")

def main():
    scheme, host, port = parse_mqtt_url(MQTT_BROKER)
    if scheme not in ("tcp",):
        log(f"[bridge] ERROR: only tcp:// supported in this tiny bridge. Got: {MQTT_BROKER}")
        sys.exit(1)

    client = mqtt.Client()
    client.on_connect = on_connect
    client.on_message = on_message

    while True:
        try:
            log(f"[bridge] Connecting to MQTT {host}:{port} ...")
            client.connect(host, port, keepalive=30)
            client.loop_forever()
        except Exception as e:
            log(f"[bridge] ERROR MQTT loop: {e} (retrying in 3s)")
            time.sleep(3)

if __name__ == "__main__":
    main()