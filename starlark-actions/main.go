package main

import (
    "context"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    ch "github.com/ClickHouse/clickhouse-go/v2"
    mqtt "github.com/eclipse/paho.mqtt.golang"
    "github.com/jackc/pgx/v5/pgxpool"
    "go.starlark.net/starlark"

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

    scriptPath := getenv("STARLARK_SCRIPT", "/starlark/actions.star")

    pgDSN := strings.TrimSpace(os.Getenv("TELEMETRY_POSTGRES_DSN")) // optional
    clickhouseDSN := strings.TrimSpace(os.Getenv("CLICKHOUSE_DSN")) // optional (external)
    clickhouseDB := getenv("CLICKHOUSE_DATABASE", "default")
    clickhouseTLS := strings.ToLower(getenv("CLICKHOUSE_TLS", "true")) == "true"

    ctx := context.Background()

    var pgpool *pgxpool.Pool
    if pgDSN != "" {
        p, err := pgxpool.New(ctx, pgDSN)
        if err != nil {
            fmt.Println("[actions] ERROR pg connect:", err)
            os.Exit(1)
        }
        pgpool = p
        defer pgpool.Close()
    }

    var chdb *ch.DB
    if clickhouseDSN != "" {
        opts, err := ch.ParseDSN(clickhouseDSN)
        if err != nil {
            fmt.Println("[actions] ERROR parse CLICKHOUSE_DSN:", err)
            os.Exit(1)
        }
        if clickhouseTLS {
            if opts.TLS == nil {
                opts.TLS = &tls.Config{InsecureSkipVerify: false}
            }
        } else {
            opts.TLS = nil
        }
        db := ch.Open(opts)
        chdb = &db
        _ = db.Ping(ctx)
        // Best effort: ensure raw_events table exists in configured database
        _, _ = db.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.raw_events (
  ts DateTime64(3, 'UTC'),
  source LowCardinality(String),
  topic String,
  payload String
)
ENGINE = MergeTree
ORDER BY (source, ts)`, clickhouseDB))
    }

    // MQTT
    mopts := mqtt.NewClientOptions().
        AddBroker(mqttBroker).
        SetClientID("starlark-actions").
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
        fmt.Println("[actions] MQTT connect retry:", tok.Error())
        time.Sleep(2 * time.Second)
    }
    fmt.Println("[actions] MQTT connected:", mqttBroker)

    builtins := starlark.StringDict{
        "mqtt_publish": starlark.NewBuiltin("mqtt_publish", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
            var topic, payload string
            if err := starlark.UnpackArgs("mqtt_publish", args, kwargs, "topic", &topic, "payload", &payload); err != nil {
                return nil, err
            }
            client.Publish(topic, 0, false, []byte(payload))
            return starlark.None, nil
        }),
        "http_post_json": starlark.NewBuiltin("http_post_json", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
            var url, payload string
            if err := starlark.UnpackArgs("http_post_json", args, kwargs, "url", &url, "payload", &payload); err != nil {
                return nil, err
            }
            req, err := http.NewRequest("POST", url, strings.NewReader(payload))
            if err != nil {
                return nil, err
            }
            req.Header.Set("Content-Type", "application/json")
            resp, err := http.DefaultClient.Do(req)
            if err != nil {
                return nil, err
            }
            defer resp.Body.Close()
            _, _ = io.ReadAll(resp.Body)
            return starlark.MakeInt(resp.StatusCode), nil
        }),
        "pg_insert_sensor_telemetry": starlark.NewBuiltin("pg_insert_sensor_telemetry", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
            if pgpool == nil {
                return starlark.None, nil
            }
            var sensorID, metric, tags, raw string
            var value float64
            if err := starlark.UnpackArgs("pg_insert_sensor_telemetry", args, kwargs,
                "sensor_id", &sensorID,
                "metric", &metric,
                "value", &value,
                "tags", &tags,
                "raw", &raw,
            ); err != nil {
                return nil, err
            }
            _, err := pgpool.Exec(ctx,
                `INSERT INTO sensor_telemetry (ts, sensor_id, metric, value, tags, raw) VALUES (now(), $1,$2,$3,$4::jsonb,$5::jsonb)`,
                sensorID, metric, value, tags, raw,
            )
            if err != nil {
                return nil, err
            }
            return starlark.None, nil
        }),
        "ch_insert_raw_event": starlark.NewBuiltin("ch_insert_raw_event", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
            if chdb == nil {
                return starlark.None, nil
            }
            var source, topic, payload string
            if err := starlark.UnpackArgs("ch_insert_raw_event", args, kwargs, "source", &source, "topic", &topic, "payload", &payload); err != nil {
                return nil, err
            }
            db := *chdb
            _, _ = db.Exec(ctx, fmt.Sprintf("INSERT INTO %s.raw_events (ts, source, topic, payload) VALUES (now(), ?, ?, ?)", clickhouseDB),
                source, topic, payload,
            )
            return starlark.None, nil
        }),
    }

    handler := func(_ mqtt.Client, msg mqtt.Message) {
        topic := msg.Topic()
        payload := msg.Payload()

        // Build event for Starlark
        event := map[string]any{
            "ts":      time.Now().UTC().Format(time.RFC3339Nano),
            "topic":   topic,
            "payload": string(payload),
            "kind":    "unknown",
        }

        if matchTopic(topic, sensorTopic) {
            event["kind"] = "sensor"
            var m map[string]any
            if err := json.Unmarshal(payload, &m); err == nil {
                event["json"] = m
            }
        } else if matchTopic(topic, chirpDeviceTopic) {
            event["kind"] = "chirpstack_device"
            if evt, err := csd.DecodeChirpStackDeviceEventJSON(topic, payload); err == nil {
                event["json"] = evt.Raw
            }
        } else if matchTopic(topic, gatewayTopic) {
            event["kind"] = "chirpstack_gateway"
            if gwEvt, err := csd.DecodeGatewayEventProtobuf(topic, payload); err == nil {
                event["gateway"] = map[string]any{
                    "region":    gwEvt.RegionHint,
                    "gatewayId": gwEvt.GatewayID,
                    "eventType": gwEvt.EventType,
                    "rawJson":   gwEvt.RawJSON,
                    "rawB64":    gwEvt.RawBase64,
                }
            } else {
                event["gateway_decode_error"] = err.Error()
            }
        }

        // Execute script fresh each event (simple + dynamic). Can be optimized with caching later.
        src, err := os.ReadFile(scriptPath)
        if err != nil {
            fmt.Println("[actions] WARN read script:", err)
            return
        }

        thread := &starlark.Thread{Name: "actions"}
        globals, err := starlark.ExecFile(thread, scriptPath, src, builtins)
        if err != nil {
            fmt.Println("[actions] WARN starlark exec:", err)
            return
        }

        fnv := globals["on_event"]
        fn, ok := fnv.(starlark.Callable)
        if !ok {
            fmt.Println("[actions] WARN script missing on_event(event)")
            return
        }

        evJSON, _ := json.Marshal(event)
        // pass as dict (converted from JSON)
        var evMap map[string]any
        _ = json.Unmarshal(evJSON, &evMap)
        evVal := toStarlark(evMap)

        if _, err := starlark.Call(thread, fn, starlark.Tuple{evVal}, nil); err != nil {
            fmt.Println("[actions] WARN on_event:", err)
        }
    }

    client.Subscribe(sensorTopic, 0, handler)
    client.Subscribe(chirpDeviceTopic, 0, handler)
    client.Subscribe(gatewayTopic, 0, handler)

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig
    fmt.Println("[actions] shutdown requested")
    client.Disconnect(250)
}

func toStarlark(v any) starlark.Value {
    switch x := v.(type) {
    case nil:
        return starlark.None
    case string:
        return starlark.String(x)
    case bool:
        if x { return starlark.True }
        return starlark.False
    case float64:
        return starlark.Float(x)
    case map[string]any:
        d := starlark.NewDict(len(x))
        for k, vv := range x {
            _ = d.SetKey(starlark.String(k), toStarlark(vv))
        }
        return d
    case []any:
        lst := make([]starlark.Value, 0, len(x))
        for _, vv := range x {
            lst = append(lst, toStarlark(vv))
        }
        return starlark.NewList(lst)
    default:
        b, _ := json.Marshal(x)
        return starlark.String(string(b))
    }
}

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
