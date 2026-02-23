\
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	mqtt "github.com/eclipse/paho.mqtt.golang"

	csd "github.com/galvarez0/Prueba-Modbus-TCP/internal/chirpstackdecode"
)

type Env struct {
	MQTTBroker string

	SensorTopic      string
	ChirpDeviceTopic string
	GatewayTopic     string

	ClickhouseDSN string
	Database     string
	TLS          bool

	BatchSize  int
	FlushEvery time.Duration
	MaxQueue   int
}

type RawEvent struct {
	TS      time.Time
	Source  string
	Topic   string
	Payload string
}

type SensorRow struct {
	TS       time.Time
	SensorID string
	Metric   string
	Value    float64
	Tags     string
	Raw      string
}

type DeviceEventRow struct {
	TS            time.Time
	ApplicationID string
	DevEUI         string
	EventType      string
	Topic          string
	JSON           string
}

type GatewayEventRow struct {
	TS       time.Time
	Region   string
	GatewayID string
	EventType string
	Topic     string
	RawJSON   string
	RawB64    string
}

func getenv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func main() {
	env := Env{
		MQTTBroker: getenv("MQTT_BROKER", "tcp://mosquitto:1883"),
		SensorTopic: getenv("SENSOR_SUB_TOPIC", "sensors/+/telemetry"),
		ChirpDeviceTopic: getenv("CHIRPSTACK_DEVICE_SUB_TOPIC", "application/+/device/+/event/+"),
		GatewayTopic: getenv("CHIRPSTACK_GW_SUB_TOPIC", "+/gateway/+/event/+"),

		// URGENTE: CONFIGURAR CLICKHOUSE_DSN PARA TU INSTANCIA EXTERNA
		ClickhouseDSN: getenv("CLICKHOUSE_DSN", "clickhouse://user:pass@host:8443/default"),
		Database:      getenv("CLICKHOUSE_DATABASE", "default"),
		TLS:           strings.ToLower(getenv("CLICKHOUSE_TLS", "true")) == "true",

		BatchSize:  mustInt(getenv("BATCH_SIZE", "500"), 500),
		FlushEvery: mustDuration(getenv("FLUSH_EVERY", "2s"), 2*time.Second),
		MaxQueue:   mustInt(getenv("MAX_QUEUE", "50000"), 50000),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts, err := clickhouse.ParseDSN(env.ClickhouseDSN)
	if err != nil {
		fmt.Println("[ingestor] ERROR parse CLICKHOUSE_DSN:", err)
		os.Exit(1)
	}
	if env.TLS {
		if opts.TLS == nil {
			opts.TLS = &tls.Config{InsecureSkipVerify: false}
		}
	} else {
		opts.TLS = nil
	}

	conn := clickhouse.Open(opts)
	if err := conn.Ping(ctx); err != nil {
		fmt.Println("[ingestor] ERROR ping ClickHouse:", err)
		os.Exit(1)
	}
	fmt.Println("[ingestor] ClickHouse connected")

	ensureTables(ctx, conn, env.Database)

	rawCh := make(chan RawEvent, env.MaxQueue)
	sensorCh := make(chan SensorRow, env.MaxQueue)
	devCh := make(chan DeviceEventRow, env.MaxQueue)
	gwCh := make(chan GatewayEventRow, env.MaxQueue)

	var wg sync.WaitGroup
	wg.Add(4)
	go batchRaw(ctx, &wg, conn, env, rawCh)
	go batchSensor(ctx, &wg, conn, env, sensorCh)
	go batchDevice(ctx, &wg, conn, env, devCh)
	go batchGateway(ctx, &wg, conn, env, gwCh)

	mopts := mqtt.NewClientOptions().
		AddBroker(env.MQTTBroker).
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
	fmt.Println("[ingestor] MQTT connected:", env.MQTTBroker)

	handler := func(_ mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		payload := msg.Payload()
		now := time.Now().UTC()

		src := classify(topic, env.SensorTopic, env.ChirpDeviceTopic, env.GatewayTopic)

		// Always store raw (best effort)
		select {
		case rawCh <- RawEvent{TS: now, Source: src, Topic: topic, Payload: string(payload)}:
		default:
		}

		if matchTopic(topic, env.SensorTopic) {
			var m map[string]any
			if err := json.Unmarshal(payload, &m); err != nil {
				return
			}
			sensorID, _ := m["sensor_id"].(string)
			metric, _ := m["metric"].(string)
			value, _ := m["value"].(float64)
			tagsB, _ := json.Marshal(m["tags"])
			rawB, _ := json.Marshal(m)
			select {
			case sensorCh <- SensorRow{TS: now, SensorID: sensorID, Metric: metric, Value: value, Tags: string(tagsB), Raw: string(rawB)}:
			default:
			}
			return
		}

		if matchTopic(topic, env.ChirpDeviceTopic) {
			evt, err := csd.DecodeChirpStackDeviceEventJSON(topic, payload)
			if err != nil {
				return
			}
			rawB, _ := json.Marshal(evt.Raw)
			select {
			case devCh <- DeviceEventRow{TS: now, ApplicationID: evt.ApplicationID, DevEUI: evt.DevEUI, EventType: evt.EventType, Topic: evt.Topic, JSON: string(rawB)}:
			default:
			}
			return
		}

		if matchTopic(topic, env.GatewayTopic) {
			gwEvt, err := csd.DecodeGatewayEventProtobuf(topic, payload)
			if err != nil {
				return
			}
			select {
			case gwCh <- GatewayEventRow{TS: now, Region: gwEvt.RegionHint, GatewayID: gwEvt.GatewayID, EventType: gwEvt.EventType, Topic: gwEvt.Topic, RawJSON: gwEvt.RawJSON, RawB64: gwEvt.RawBase64}:
			default:
			}
			return
		}
	}

	client.Subscribe(env.SensorTopic, 0, handler)
	client.Subscribe(env.ChirpDeviceTopic, 0, handler)
	client.Subscribe(env.GatewayTopic, 0, handler)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("[ingestor] shutdown requested")

	client.Disconnect(250)
	cancel()

	time.Sleep(500 * time.Millisecond)
	close(rawCh)
	close(sensorCh)
	close(devCh)
	close(gwCh)
	wg.Wait()
}

func ensureTables(ctx context.Context, conn clickhouse.Conn, db string) {
	mustExec(ctx, conn, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.raw_events (
  ts DateTime64(3, 'UTC'),
  source LowCardinality(String),
  topic String,
  payload String
)
ENGINE = MergeTree
ORDER BY (source, ts)`, db))

	mustExec(ctx, conn, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.sensor_telemetry (
  ts DateTime64(3, 'UTC'),
  sensor_id String,
  metric LowCardinality(String),
  value Float64,
  tags String,
  raw String
)
ENGINE = MergeTree
ORDER BY (sensor_id, ts)`, db))

	mustExec(ctx, conn, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.chirpstack_device_events (
  ts DateTime64(3, 'UTC'),
  application_id String,
  dev_eui String,
  event_type LowCardinality(String),
  topic String,
  json String
)
ENGINE = MergeTree
ORDER BY (dev_eui, ts)`, db))

	mustExec(ctx, conn, fmt.Sprintf(`
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
ORDER BY (gateway_id, ts)`, db))
}

func mustExec(ctx context.Context, conn clickhouse.Conn, query string) {
	if err := conn.Exec(ctx, query); err != nil {
		fmt.Println("[ingestor] WARN ddl:", err)
	}
}

func batchRaw(ctx context.Context, wg *sync.WaitGroup, conn clickhouse.Conn, env Env, ch <-chan RawEvent) {
	defer wg.Done()
	flush := time.NewTicker(env.FlushEvery)
	defer flush.Stop()

	buf := make([]RawEvent, 0, env.BatchSize)
	for {
		select {
		case <-ctx.Done():
			flushRaw(ctx, conn, env, buf)
			return
		case row, ok := <-ch:
			if !ok {
				flushRaw(ctx, conn, env, buf)
				return
			}
			buf = append(buf, row)
			if len(buf) >= env.BatchSize {
				flushRaw(ctx, conn, env, buf)
				buf = buf[:0]
			}
		case <-flush.C:
			if len(buf) > 0 {
				flushRaw(ctx, conn, env, buf)
				buf = buf[:0]
			}
		}
	}
}

func flushRaw(ctx context.Context, conn clickhouse.Conn, env Env, rows []RawEvent) {
	if len(rows) == 0 {
		return
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.raw_events (ts, source, topic, payload)", env.Database))
	if err != nil {
		fmt.Println("[ingestor] WARN raw prepare:", err)
		return
	}
	for _, r := range rows {
		_ = batch.Append(r.TS, r.Source, r.Topic, r.Payload)
	}
	if err := batch.Send(); err != nil {
		fmt.Println("[ingestor] WARN raw send:", err)
	}
}

func batchSensor(ctx context.Context, wg *sync.WaitGroup, conn clickhouse.Conn, env Env, ch <-chan SensorRow) {
	defer wg.Done()
	flush := time.NewTicker(env.FlushEvery)
	defer flush.Stop()

	buf := make([]SensorRow, 0, env.BatchSize)
	for {
		select {
		case <-ctx.Done():
			flushSensor(ctx, conn, env, buf)
			return
		case row, ok := <-ch:
			if !ok {
				flushSensor(ctx, conn, env, buf)
				return
			}
			buf = append(buf, row)
			if len(buf) >= env.BatchSize {
				flushSensor(ctx, conn, env, buf)
				buf = buf[:0]
			}
		case <-flush.C:
			if len(buf) > 0 {
				flushSensor(ctx, conn, env, buf)
				buf = buf[:0]
			}
		}
	}
}

func flushSensor(ctx context.Context, conn clickhouse.Conn, env Env, rows []SensorRow) {
	if len(rows) == 0 {
		return
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.sensor_telemetry (ts, sensor_id, metric, value, tags, raw)", env.Database))
	if err != nil {
		fmt.Println("[ingestor] WARN sensor prepare:", err)
		return
	}
	for _, r := range rows {
		_ = batch.Append(r.TS, r.SensorID, r.Metric, r.Value, r.Tags, r.Raw)
	}
	if err := batch.Send(); err != nil {
		fmt.Println("[ingestor] WARN sensor send:", err)
	}
}

func batchDevice(ctx context.Context, wg *sync.WaitGroup, conn clickhouse.Conn, env Env, ch <-chan DeviceEventRow) {
	defer wg.Done()
	flush := time.NewTicker(env.FlushEvery)
	defer flush.Stop()

	buf := make([]DeviceEventRow, 0, env.BatchSize)
	for {
		select {
		case <-ctx.Done():
			flushDevice(ctx, conn, env, buf)
			return
		case row, ok := <-ch:
			if !ok {
				flushDevice(ctx, conn, env, buf)
				return
			}
			buf = append(buf, row)
			if len(buf) >= env.BatchSize {
				flushDevice(ctx, conn, env, buf)
				buf = buf[:0]
			}
		case <-flush.C:
			if len(buf) > 0 {
				flushDevice(ctx, conn, env, buf)
				buf = buf[:0]
			}
		}
	}
}

func flushDevice(ctx context.Context, conn clickhouse.Conn, env Env, rows []DeviceEventRow) {
	if len(rows) == 0 {
		return
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.chirpstack_device_events (ts, application_id, dev_eui, event_type, topic, json)", env.Database))
	if err != nil {
		fmt.Println("[ingestor] WARN device prepare:", err)
		return
	}
	for _, r := range rows {
		_ = batch.Append(r.TS, r.ApplicationID, r.DevEUI, r.EventType, r.Topic, r.JSON)
	}
	if err := batch.Send(); err != nil {
		fmt.Println("[ingestor] WARN device send:", err)
	}
}

func batchGateway(ctx context.Context, wg *sync.WaitGroup, conn clickhouse.Conn, env Env, ch <-chan GatewayEventRow) {
	defer wg.Done()
	flush := time.NewTicker(env.FlushEvery)
	defer flush.Stop()

	buf := make([]GatewayEventRow, 0, env.BatchSize)
	for {
		select {
		case <-ctx.Done():
			flushGateway(ctx, conn, env, buf)
			return
		case row, ok := <-ch:
			if !ok {
				flushGateway(ctx, conn, env, buf)
				return
			}
			buf = append(buf, row)
			if len(buf) >= env.BatchSize {
				flushGateway(ctx, conn, env, buf)
				buf = buf[:0]
			}
		case <-flush.C:
			if len(buf) > 0 {
				flushGateway(ctx, conn, env, buf)
				buf = buf[:0]
			}
		}
	}
}

func flushGateway(ctx context.Context, conn clickhouse.Conn, env Env, rows []GatewayEventRow) {
	if len(rows) == 0 {
		return
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.chirpstack_gateway_events (ts, region, gateway_id, event_type, topic, raw_json, raw_b64)", env.Database))
	if err != nil {
		fmt.Println("[ingestor] WARN gw prepare:", err)
		return
	}
	for _, r := range rows {
		_ = batch.Append(r.TS, r.Region, r.GatewayID, r.EventType, r.Topic, r.RawJSON, r.RawB64)
	}
	if err := batch.Send(); err != nil {
		fmt.Println("[ingestor] WARN gw send:", err)
	}
}

func classify(topic, sensor, dev, gw string) string {
	if matchTopic(topic, sensor) {
		return "sensor"
	}
	if matchTopic(topic, dev) {
		return "chirpstack_device"
	}
	if matchTopic(topic, gw) {
		return "chirpstack_gateway"
	}
	return "unknown"
}

func matchTopic(topic, pattern string) bool {
	t := strings.Split(topic, "/")
	p := strings.Split(pattern, "/")
	for i := 0; i < len(p); i++ {
		if i >= len(t) {
			return false
		}
		if p[i] == "#" {
			return true
		}
		if p[i] == "+" {
			continue
		}
		if p[i] != t[i] {
			return false
		}
	}
	return len(t) == len(p)
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
	if i <= 0 {
		return def
	}
	return i
}