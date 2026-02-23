\
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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

type Config struct {
	MQTTBroker string
	TopicBase  string

	PublishEvery time.Duration
	JitterPct    float64 // 0.0..1.0

	SensorIDs    []string // explicit list OR generated from SensorCount
	SensorCount  int
	SensorPrefix string

	Metrics         []string // temperature_c, cpu_pct, mem_pct, disk_iops
	HeartbeatEvery  time.Duration
	PostgresDSN     string
	DisablePostgres bool

	RandomSeed int64 // 0 => time based
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

func parseDurationOr(key, def string) time.Duration {
	d, err := time.ParseDuration(getenv(key, def))
	if err != nil {
		return mustDuration(def)
	}
	return d
}

func mustDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

func parseFloatOr(key, def string) float64 {
	v := getenv(key, def)
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		f, _ = strconv.ParseFloat(def, 64)
	}
	return f
}

func parseIntOr(key, def string) int {
	v := getenv(key, def)
	i, err := strconv.Atoi(v)
	if err != nil {
		i, _ = strconv.Atoi(def)
	}
	return i
}

func main() {
	cfg := Config{
		MQTTBroker: getenv("MQTT_BROKER", "tcp://mosquitto:1883"),
		TopicBase:  getenv("SENSOR_TOPIC_BASE", "sensors"),

		PublishEvery: parseDurationOr("PUBLISH_EVERY", "5s"),
		JitterPct:    clamp01(parseFloatOr("PUBLISH_JITTER_PCT", "0.10")),

		SensorIDs:    parseCSV(getenv("SENSOR_IDS", "")),
		SensorCount:  parseIntOr("SENSOR_COUNT", "3"),
		SensorPrefix: getenv("SENSOR_PREFIX", "gw"),

		Metrics:        parseCSV(getenv("SENSOR_METRICS", "temperature_c,cpu_pct,mem_pct,disk_iops")),
		HeartbeatEvery: parseDurationOr("HEARTBEAT_EVERY", "30s"),

		PostgresDSN:     getenv("TELEMETRY_POSTGRES_DSN", "postgresql://telemetry:telemetry@telemetry-postgresql/telemetry?sslmode=disable"),
		DisablePostgres: strings.ToLower(getenv("DISABLE_POSTGRES", "false")) == "true",

		RandomSeed: parseSeed(getenv("RANDOM_SEED", "")),
	}

	if len(cfg.SensorIDs) == 0 {
		cfg.SensorIDs = make([]string, 0, cfg.SensorCount)
		for i := 1; i <= cfg.SensorCount; i++ {
			cfg.SensorIDs = append(cfg.SensorIDs, fmt.Sprintf("%s-%d", cfg.SensorPrefix, i))
		}
	}

	if cfg.RandomSeed == 0 {
		rand.Seed(time.Now().UnixNano())
	} else {
		rand.Seed(cfg.RandomSeed)
	}

	ctx := context.Background()

	var pool *pgxpool.Pool
	if !cfg.DisablePostgres {
		p, err := pgxpool.New(ctx, cfg.PostgresDSN)
		if err != nil {
			fmt.Println("[sensor-gw] ERROR pg connect:", err)
			os.Exit(1)
		}
		pool = p
		defer pool.Close()
	}

	mopts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTBroker).
		SetClientID("sensor-gateway-go").
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
		fmt.Println("[sensor-gw] MQTT connect retry:", tok.Error())
		time.Sleep(2 * time.Second)
	}
	fmt.Println("[sensor-gw] MQTT connected:", cfg.MQTTBroker)

	// Publish meta once
	for _, sid := range cfg.SensorIDs {
		meta := map[string]any{
			"sensor_id": sid,
			"metrics":   cfg.Metrics,
			"note":      "Simulated gateway sensor meta",
		}
		b, _ := json.Marshal(meta)
		client.Publish(fmt.Sprintf("%s/%s/meta", cfg.TopicBase, sid), 0, true, b)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	publishTick := time.NewTicker(cfg.PublishEvery)
	heartbeatTick := time.NewTicker(cfg.HeartbeatEvery)
	defer publishTick.Stop()
	defer heartbeatTick.Stop()

	start := time.Now()

	for {
		select {
		case <-publishTick.C:
			// Per-sensor publish with jitter to avoid bursts
			for _, sid := range cfg.SensorIDs {
				sleepJitter(cfg.PublishEvery, cfg.JitterPct)

				events := generateMetrics(sid, cfg.Metrics, start)
				for _, evt := range events {
					topic := fmt.Sprintf("%s/%s/telemetry", cfg.TopicBase, sid)
					b, _ := json.Marshal(evt)
					client.Publish(topic, 0, false, b)

					if pool != nil {
						_, err := pool.Exec(ctx,
							`INSERT INTO sensor_telemetry (ts, sensor_id, metric, value, tags, raw)
                             VALUES ($1,$2,$3,$4,$5,$6)`,
							evt.TS, evt.SensorID, evt.Metric, evt.Value, evt.Tags, evt.Raw,
						)
						if err != nil {
							fmt.Println("[sensor-gw] WARN pg insert:", err)
						}
					}
				}
			}

		case <-heartbeatTick.C:
			hb := map[string]any{
				"ts":           time.Now().UTC().Format(time.RFC3339Nano),
				"type":         "heartbeat",
				"sensor_count": len(cfg.SensorIDs),
			}
			b, _ := json.Marshal(hb)
			client.Publish(fmt.Sprintf("%s/heartbeat", cfg.TopicBase), 0, false, b)

		case <-sig:
			fmt.Println("[sensor-gw] shutdown requested")
			client.Disconnect(250)
			return
		}
	}
}

func parseSeed(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	// accept any string and hash it for deterministic demo runs
	h := sha1.Sum([]byte(s))
	return int64(uint64(h[0])<<56 | uint64(h[1])<<48 | uint64(h[2])<<40 | uint64(h[3])<<32 | uint64(h[4])<<24 | uint64(h[5])<<16 | uint64(h[6])<<8 | uint64(h[7]))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func sleepJitter(base time.Duration, pct float64) {
	if pct <= 0 {
		return
	}
	max := float64(base) * pct
	delta := (rand.Float64()*2 - 1) * max
	d := time.Duration(delta)
	if d < 0 {
		time.Sleep(-d)
		return
	}
	time.Sleep(d)
}

func generateMetrics(sensorID string, metrics []string, start time.Time) []Telemetry {
	out := make([]Telemetry, 0, len(metrics))
	now := time.Now().UTC()
	for _, m := range metrics {
		switch m {
		case "temperature_c":
			out = append(out, Telemetry{
				TS: now, SensorID: sensorID, Metric: "temperature_c", Value: serverLikeTemp(start),
				Tags: map[string]any{"unit": "celsius", "source": "simulated"},
				Raw:  map[string]any{"model": "sine+noise"},
			})
		case "cpu_pct":
			out = append(out, Telemetry{
				TS: now, SensorID: sensorID, Metric: "cpu_pct", Value: cpuLike(start),
				Tags: map[string]any{"unit": "percent", "source": "simulated"},
				Raw:  map[string]any{"model": "bursty"},
			})
		case "mem_pct":
			out = append(out, Telemetry{
				TS: now, SensorID: sensorID, Metric: "mem_pct", Value: memLike(start),
				Tags: map[string]any{"unit": "percent", "source": "simulated"},
				Raw:  map[string]any{"model": "slow_drift"},
			})
		case "disk_iops":
			out = append(out, Telemetry{
				TS: now, SensorID: sensorID, Metric: "disk_iops", Value: diskIOPSLike(start),
				Tags: map[string]any{"unit": "iops", "source": "simulated"},
				Raw:  map[string]any{"model": "spiky"},
			})
		}
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func serverLikeTemp(start time.Time) float64 {
	elapsed := time.Since(start).Seconds()
	base := 55.0
	wave := 8.0 * math.Sin(elapsed/60.0)
	noise := rand.NormFloat64() * 0.7
	v := base + wave + noise
	if v < 30 {
		v = 30
	}
	if v > 95 {
		v = 95
	}
	return round1(v)
}

func cpuLike(start time.Time) float64 {
	elapsed := time.Since(start).Seconds()
	base := 20.0 + 10.0*math.Sin(elapsed/15.0)
	burst := 0.0
	if rand.Float64() < 0.12 {
		burst = 40 + rand.Float64()*40
	}
	v := base + burst + rand.NormFloat64()*3
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return round1(v)
}

func memLike(start time.Time) float64 {
	elapsed := time.Since(start).Seconds()
	drift := 10.0 * math.Sin(elapsed/120.0)
	v := 55.0 + drift + rand.NormFloat64()*1.5
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return round1(v)
}

func diskIOPSLike(start time.Time) float64 {
	elapsed := time.Since(start).Seconds()
	base := 80.0 + 30.0*math.Sin(elapsed/20.0)
	spike := 0.0
	if rand.Float64() < 0.08 {
		spike = 200 + rand.Float64()*500
	}
	v := base + spike + rand.NormFloat64()*10
	if v < 0 {
		v = 0
	}
	return round1(v)
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:4])
}
