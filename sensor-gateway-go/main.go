package main

import (
    "context"
    "encoding/json"
    "fmt"
    "math"
    "math/rand"
    "os"
    "os/signal"
    "strconv"
    "strings"
    "syscall"
    "time"

    mqtt "github.com/eclipse/paho.mqtt.golang"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Telemetry struct {
    TS       time.Time      `json:"ts"`
    SensorID string         `json:"sensor_id"`
    Metric   string         `json:"metric"`
    Value    float64        `json:"value"`
    Tags     map[string]any `json:"tags,omitempty"`
    Raw      map[string]any `json:"raw"`
}

func getenv(key, def string) string {
    if v := strings.TrimSpace(os.Getenv(key)); v != "" {
        return v
    }
    return def
}

func main() {
    mqttBroker := getenv("MQTT_BROKER", "tcp://mosquitto:1883")
    mqttTopicBase := getenv("SENSOR_TOPIC_BASE", "sensors") // sensors/<id>/telemetry
    intervalStr := getenv("PUBLISH_EVERY", "5s")
    interval, err := time.ParseDuration(intervalStr)
    if err != nil {
        interval = 5 * time.Second
    }

    // Separate telemetry Postgres
    pgDSN := getenv("TELEMETRY_POSTGRES_DSN", "postgresql://telemetry:telemetry@telemetry-postgresql/telemetry?sslmode=disable")

    sensorIDsCSV := getenv("SENSOR_IDS", "gw-1,gw-2,gw-3")
    sensorIDs := strings.Split(sensorIDsCSV, ",")
    for i := range sensorIDs {
        sensorIDs[i] = strings.TrimSpace(sensorIDs[i])
    }

    seedStr := getenv("RANDOM_SEED", "")
    if seedStr == "" {
        rand.Seed(time.Now().UnixNano())
    } else if s, err := strconv.ParseInt(seedStr, 10, 64); err == nil {
        rand.Seed(s)
    } else {
        rand.Seed(time.Now().UnixNano())
    }

    ctx := context.Background()

    pool, err := pgxpool.New(ctx, pgDSN)
    if err != nil {
        fmt.Println("[sensor-gw] ERROR pg connect:", err)
        os.Exit(1)
    }
    defer pool.Close()

    opts := mqtt.NewClientOptions().
        AddBroker(mqttBroker).
        SetClientID("sensor-gateway-go").
        SetAutoReconnect(true).
        SetConnectRetry(true).
        SetConnectRetryInterval(3 * time.Second)

    client := mqtt.NewClient(opts)
    for {
        tok := client.Connect()
        tok.Wait()
        if tok.Error() == nil {
            break
        }
        fmt.Println("[sensor-gw] MQTT connect retry:", tok.Error())
        time.Sleep(2 * time.Second)
    }
    fmt.Println("[sensor-gw] MQTT connected:", mqttBroker)

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    start := time.Now()

    for {
        select {
        case <-ticker.C:
            for _, sid := range sensorIDs {
                temp := generateServerLikeTemp(start)
                evt := Telemetry{
                    TS:       time.Now().UTC(),
                    SensorID: sid,
                    Metric:   "temperature_c",
                    Value:    temp,
                    Tags: map[string]any{
                        "source": "simulated",
                        "unit":   "celsius",
                    },
                    Raw: map[string]any{
                        "note": "Simulated server temperature-like signal.",
                    },
                }

                b, _ := json.Marshal(evt)
                topic := fmt.Sprintf("%s/%s/telemetry", mqttTopicBase, sid)
                client.Publish(topic, 0, false, b)

                _, err := pool.Exec(ctx,
                    `INSERT INTO sensor_telemetry (ts, sensor_id, metric, value, tags, raw) VALUES ($1,$2,$3,$4,$5,$6)`,
                    evt.TS, evt.SensorID, evt.Metric, evt.Value, evt.Tags, evt.Raw,
                )
                if err != nil {
                    fmt.Println("[sensor-gw] WARN pg insert:", err)
                }
            }

        case <-sig:
            fmt.Println("[sensor-gw] shutdown requested")
            client.Disconnect(250)
            return
        }
    }
}

func generateServerLikeTemp(start time.Time) float64 {
    elapsed := time.Since(start).Seconds()
    base := 55.0
    wave := 8.0 * math.Sin(elapsed/60.0) // ~1 minute wave for demo
    noise := rand.NormFloat64() * 0.7
    v := base + wave + noise
    if v < 30 { v = 30 }
    if v > 95 { v = 95 }
    return math.Round(v*10) / 10
}
