package main

import (
    "context"
    "crypto/tls"
    "database/sql"
    "encoding/json"
    "fmt"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    ch "github.com/ClickHouse/clickhouse-go/v2"
    mqtt "github.com/eclipse/paho.mqtt.golang"

    csd "github.com/galvarez0/Prueba-Modbus-TCP/internal/chirpstackdecode"
)

func getenv(key, def string) string {
    v := strings.TrimSpace(os.Getenv(key))
    if v == "" {
        return def
    }
    return v
}

func main() {
    mqttBroker := getenv("MQTT_BROKER", "tcp://mosquitto:1883")
    sensorTopic := getenv("SENSOR_SUB_TOPIC", "sensors/+/telemetry")
    chirpDeviceTopic := getenv("CHIRPSTACK_DEVICE_SUB_TOPIC", "application/+/device/+/event/+")
    gatewayTopic := getenv("CHIRPSTACK_GW_SUB_TOPIC", "+/gateway/+/event/+")

    // URGENTE: CONFIGURAR CLICKHOUSE_DSN PARA TU CLICKHOUSE EXTERNO
    clickhouseDSN := getenv("CLICKHOUSE_DSN", "clickhouse://user:pass@host:8443/default")
    database := getenv("CLICKHOUSE_DATABASE", "default")
    useTLS := strings.ToLower(getenv("CLICKHOUSE_TLS", "true")) == "true"

    ctx := context.Background()

    opts, err := ch.ParseDSN(clickhouseDSN)
    if err != nil {
        fmt.Println("[ingestor] ERROR parse CLICKHOUSE_DSN:", err)
        os.Exit(1)
    }
    if useTLS {
        if opts.TLS == nil {
            opts.TLS = &tls.Config{InsecureSkipVerify: false}
        }
    } else {
        opts.TLS = nil
    }

    db := ch.OpenDB(opts)
    if err := db.PingContext(ctx); err != nil {
        fmt.Println("[ingestor] ERROR ping ClickHouse:", err)
        os.Exit(1)
    }
    fmt.Println("[ingestor] ClickHouse connected")

    ensureTables(ctx, db, database)

    // MQTT
    mopts := mqtt.NewClientOptions().
        AddBroker(mqttBroker).
        SetClientID("clickhouse-ingestor").
        SetAutoReconnect(true).
        SetConnectRetry(true).
        SetConnectRetryInterval(3 * time.Second)

    client := mqtt.NewClient(mopts)
    for {
        tok := client.Connect()
        tok.Wait()
        if tok.Error() == nil {
            break
        }
        fmt.Println("[ingestor] MQTT connect retry:", tok.Error())
        time.Sleep(2 * time.Second)
    }
    fmt.Println("[ingestor] MQTT connected:", mqttBroker)

    handler := func(_ mqtt.Client, msg mqtt.Message) {
        topic := msg.Topic()
        payload := msg.Payload()
        now := time.Now().UTC()

        // raw always
        exec(ctx, db,
            fmt.Sprintf("INSERT INTO %s.raw_events (ts, source, topic, payload) VALUES (?, ?, ?, ?)", database),
            now, classify(topic, sensorTopic, chirpDeviceTopic, gatewayTopic), topic, string(payload),
        )

        if matchTopic(topic, sensorTopic) {
            var m map[string]any
            if err := json.Unmarshal(payload, &m); err != nil {
                return
            }
            sensorID, _ := m["sensor_id"].(string)
            metric, _ := m["metric"].(string)
            value, _ := m["value"].(float64)
            tagsB, _ := json.Marshal(m["tags"])
            rawB, _ := json.Marshal(m)

            exec(ctx, db,
                fmt.Sprintf("INSERT INTO %s.sensor_telemetry (ts, sensor_id, metric, value, tags, raw) VALUES (?, ?, ?, ?, ?, ?)", database),
                now, sensorID, metric, value, string(tagsB), string(rawB),
            )
            return
        }

        if matchTopic(topic, chirpDeviceTopic) {
            evt, err := csd.DecodeChirpStackDeviceEventJSON(topic, payload)
            if err != nil {
                return
            }
            rawB, _ := json.Marshal(evt.Raw)
            exec(ctx, db,
                fmt.Sprintf("INSERT INTO %s.chirpstack_device_events (ts, application_id, dev_eui, event_type, topic, json) VALUES (?, ?, ?, ?, ?, ?)", database),
                now, evt.ApplicationID, evt.DevEUI, evt.EventType, evt.Topic, string(rawB),
            )
            return
        }

        if matchTopic(topic, gatewayTopic) {
            gwEvt, _ := csd.DecodeGatewayEventProtobuf(topic, payload) // store best-effort
            if gwEvt == nil {
                return
            }
            exec(ctx, db,
                fmt.Sprintf("INSERT INTO %s.chirpstack_gateway_events (ts, region, gateway_id, event_type, topic, raw_json, raw_b64) VALUES (?, ?, ?, ?, ?, ?, ?)", database),
                now, gwEvt.RegionHint, gwEvt.GatewayID, gwEvt.EventType, gwEvt.Topic, gwEvt.RawJSON, gwEvt.RawBase64,
            )
            return
        }
    }

    client.Subscribe(sensorTopic, 0, handler)
    client.Subscribe(chirpDeviceTopic, 0, handler)
    client.Subscribe(gatewayTopic, 0, handler)

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig
    fmt.Println("[ingestor] shutdown requested")
    client.Disconnect(250)
}

func ensureTables(ctx context.Context, db *sql.DB, database string) {
    exec(ctx, db, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.raw_events (
  ts DateTime64(3, 'UTC'),
  source LowCardinality(String),
  topic String,
  payload String
)
ENGINE = MergeTree
ORDER BY (source, ts)`, database))

    exec(ctx, db, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.sensor_telemetry (
  ts DateTime64(3, 'UTC'),
  sensor_id String,
  metric LowCardinality(String),
  value Float64,
  tags String,
  raw String
)
ENGINE = MergeTree
ORDER BY (sensor_id, ts)`, database))

    exec(ctx, db, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.chirpstack_device_events (
  ts DateTime64(3, 'UTC'),
  application_id String,
  dev_eui String,
  event_type LowCardinality(String),
  topic String,
  json String
)
ENGINE = MergeTree
ORDER BY (dev_eui, ts)`, database))

    exec(ctx, db, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.chirpstack_gateway_events (
  ts DateTime64(3, 'UTC'),
  region String,
  gateway_id String,
  event_type LowCardinality(String),
  topic String,
  raw_json String,
  raw_b64 String
)
ENGINE = MergeTree
ORDER BY (gateway_id, ts)`, database))
}

func exec(ctx context.Context, db *sql.DB, query string, args ...any) {
    if _, err := db.ExecContext(ctx, query, args...); err != nil {
        fmt.Println("[ingestor] WARN:", err)
    }
}

func classify(topic, sensor, dev, gw string) string {
    if matchTopic(topic, sensor) { return "sensor" }
    if matchTopic(topic, dev) { return "chirpstack_device" }
    if matchTopic(topic, gw) { return "chirpstack_gateway" }
    return "unknown"
}

// Minimal MQTT wildcard matching for + and #
func matchTopic(topic, pattern string) bool {
    t := strings.Split(topic, "/")
    p := strings.Split(pattern, "/")
    for i := 0; i < len(p); i++ {
        if i >= len(t) { return false }
        if p[i] == "#" { return true }
        if p[i] == "+" { continue }
        if p[i] != t[i] { return false }
    }
    return len(t) == len(p)
}
