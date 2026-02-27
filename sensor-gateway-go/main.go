\
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
	TS       string         `json:"ts"`
	SensorID string         `json:"sensor_id"`
	Metric   string         `json:"metric"`
	Value    float64        `json:"value"`
	Tags     map[string]any `json:"tags,omitempty"`
	Raw      map[string]any `json:"raw,omitempty"`
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mustDuration(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return d
}

func mustInt(s string, def int) int {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return i
}

func main() {
	mqttBroker := getenv("MQTT_BROKER", "tcp://mosquitto:1883")
	base := getenv("SENSOR_TOPIC_BASE", "sensors")
	publishEvery := mustDuration(getenv("PUBLISH_EVERY", "5s"), 5*time.Second)
	sensorIDs := parseCSV(getenv("SENSOR_IDS", "gw-1,gw-2,gw-3"))
	metrics := parseCSV(getenv("SENSOR_METRICS", "temperature_c,cpu_pct,mem_pct,disk_iops"))

	pgDSN := strings.TrimSpace(os.Getenv("TELEMETRY_POSTGRES_DSN"))
	disablePG := strings.ToLower(getenv("DISABLE_POSTGRES", "false")) == "true"

	rand.Seed(time.Now().UnixNano())
	start := time.Now()

	ctx := context.Background()
	var pg *pgxpool.Pool
	if !disablePG && pgDSN != "" {
		p, err := pgxpool.New(ctx, pgDSN)
		if err != nil {
			fmt.Println("[sensor] ERROR pg connect:", err)
			os.Exit(1)
		}
		pg = p
		defer pg.Close()

		// Create table if needed (safe)
		_, _ = pg.Exec(ctx, `
CREATE TABLE IF NOT EXISTS sensor_telemetry (
  ts timestamptz NOT NULL,
  sensor_id text NOT NULL,
  metric text NOT NULL,
  value double precision NOT NULL,
  tags jsonb,
  raw jsonb
);
CREATE INDEX IF NOT EXISTS idx_sensor_telemetry_sensor_ts ON sensor_telemetry (sensor_id, ts);
`)
	}

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
		fmt.Println("[sensor] MQTT retry:", tok.Error())
		time.Sleep(2 * time.Second)
	}
	fmt.Println("[sensor] MQTT connected:", mqttBroker)

	ticker := time.NewTicker(publishEvery)
	defer ticker.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			now := time.Now().UTC().Format(time.RFC3339Nano)
			for _, sid := range sensorIDs {
				for _, m := range metrics {
					val := simulate(m, time.Since(start).Seconds())
					ev := Telemetry{
						TS:       now,
						SensorID: sid,
						Metric:   m,
						Value:    val,
						Tags: map[string]any{
							"source": "simulated",
						},
						Raw: map[string]any{
							"model": "simple",
						},
					}
					b, _ := json.Marshal(ev)
					topic := fmt.Sprintf("%s/%s/telemetry", base, sid)
					client.Publish(topic, 0, false, b)

					if pg != nil {
						_, _ = pg.Exec(ctx,
							`INSERT INTO sensor_telemetry (ts, sensor_id, metric, value, tags, raw)
                             VALUES (now(), $1,$2,$3,$4::jsonb,$5::jsonb)`,
							sid, m, val, toJSON(ev.Tags), toJSON(ev),
						)
					}
				}
			}

		case <-sig:
			fmt.Println("[sensor] shutdown")
			client.Disconnect(250)
			return
		}
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func simulate(metric string, t float64) float64 {
	noise := rand.NormFloat64()
	switch metric {
	case "temperature_c":
		v := 55 + 8*math.Sin(t/60) + noise*0.7
		return clamp(round1(v), 30, 95)
	case "cpu_pct":
		v := 25 + 15*math.Sin(t/12) + noise*3
		if rand.Float64() < 0.12 {
			v += 40 + rand.Float64()*40
		}
		return clamp(round1(v), 0, 100)
	case "mem_pct":
		v := 55 + 10*math.Sin(t/120) + noise*1.5
		return clamp(round1(v), 0, 100)
	case "disk_iops":
		v := 80 + 30*math.Sin(t/20) + noise*10
		if rand.Float64() < 0.08 {
			v += 200 + rand.Float64()*500
		}
		return clamp(round1(v), 0, 2000)
	default:
		return round1(10 + rand.Float64()*5)
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
