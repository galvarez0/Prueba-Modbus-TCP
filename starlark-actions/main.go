\
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.starlark.net/starlark"

	csd "github.com/galvarez0/Prueba-Modbus-TCP/internal/chirpstackdecode"
)

type Env struct {
	MQTTBroker string

	SensorTopic      string
	ChirpDeviceTopic string
	GatewayTopic     string

	ScriptPath string

	PostgresDSN   string
	ClickhouseDSN string
	ClickhouseDB  string
	ClickhouseTLS bool

	MaxPayloadBytes int
	ExecTimeout     time.Duration
	ErrorTopic      string
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
		MQTTBroker:       getenv("MQTT_BROKER", "tcp://mosquitto:1883"),
		SensorTopic:      getenv("SENSOR_SUB_TOPIC", "sensors/+/telemetry"),
		ChirpDeviceTopic: getenv("CHIRPSTACK_DEVICE_SUB_TOPIC", "application/+/device/+/event/+"),
		GatewayTopic:     getenv("CHIRPSTACK_GW_SUB_TOPIC", "+/gateway/+/event/+"),

		ScriptPath: getenv("STARLARK_SCRIPT", "/starlark/actions.star"),

		PostgresDSN:   strings.TrimSpace(os.Getenv("TELEMETRY_POSTGRES_DSN")),
		ClickhouseDSN: strings.TrimSpace(os.Getenv("CLICKHOUSE_DSN")),
		ClickhouseDB:  getenv("CLICKHOUSE_DATABASE", "default"),
		ClickhouseTLS: strings.ToLower(getenv("CLICKHOUSE_TLS", "true")) == "true",

		MaxPayloadBytes: mustInt(getenv("MAX_PAYLOAD_BYTES", "1048576"), 1048576),
		ExecTimeout:     mustDuration(getenv("EXEC_TIMEOUT", "250ms"), 250*time.Millisecond),
		ErrorTopic:      getenv("ACTIONS_ERROR_TOPIC", "actions/errors"),
	}

	ctx := context.Background()

	var pgpool *pgxpool.Pool
	if env.PostgresDSN != "" {
		p, err := pgxpool.New(ctx, env.PostgresDSN)
		if err != nil {
			fmt.Println("[actions] ERROR pg connect:", err)
			os.Exit(1)
		}
		pgpool = p
		defer pgpool.Close()
	}

	var chConn clickhouse.Conn
	if env.ClickhouseDSN != "" {
		opts, err := clickhouse.ParseDSN(env.ClickhouseDSN)
		if err != nil {
			fmt.Println("[actions] ERROR parse CLICKHOUSE_DSN:", err)
			os.Exit(1)
		}
		if env.ClickhouseTLS {
			if opts.TLS == nil {
				opts.TLS = &tls.Config{InsecureSkipVerify: false}
			}
		} else {
			opts.TLS = nil
		}
		chConn = clickhouse.Open(opts)
		_ = chConn.Ping(ctx)
		_ = chConn.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.raw_events (
  ts DateTime64(3, 'UTC'),
  source LowCardinality(String),
  topic String,
  payload String
)
ENGINE = MergeTree
ORDER BY (source, ts)`, env.ClickhouseDB))
	}

	mopts := mqtt.NewClientOptions().
		AddBroker(env.MQTTBroker).
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
	fmt.Println("[actions] MQTT connected:", env.MQTTBroker)

	runner := NewScriptRunner(env.ScriptPath)

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
				`INSERT INTO sensor_telemetry (ts, sensor_id, metric, value, tags, raw)
                 VALUES (now(), $1,$2,$3,$4::jsonb,$5::jsonb)`,
				sensorID, metric, value, tags, raw,
			)
			if err != nil {
				return nil, err
			}
			return starlark.None, nil
		}),
		"ch_insert_raw_event": starlark.NewBuiltin("ch_insert_raw_event", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			if chConn == nil {
				return starlark.None, nil
			}
			var source, topic, payload string
			if err := starlark.UnpackArgs("ch_insert_raw_event", args, kwargs, "source", &source, "topic", &topic, "payload", &payload); err != nil {
				return nil, err
			}
			_ = chConn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.raw_events (ts, source, topic, payload) VALUES (now(), ?, ?, ?)", env.ClickhouseDB),
				source, topic, payload,
			)
			return starlark.None, nil
		}),
	}

	handler := func(_ mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		payload := msg.Payload()
		if len(payload) > env.MaxPayloadBytes {
			publishError(client, env.ErrorTopic, topic, fmt.Sprintf("payload too large: %d bytes", len(payload)))
			return
		}

		event := buildEvent(topic, payload, env.SensorTopic, env.ChirpDeviceTopic, env.GatewayTopic)

		prog, err := runner.LoadIfChanged(builtins)
		if err != nil {
			publishError(client, env.ErrorTopic, topic, "starlark load error: "+err.Error())
			return
		}

		done := make(chan error, 1)
		go func() { done <- prog.CallOnEvent(event) }()

		select {
		case err := <-done:
			if err != nil {
				publishError(client, env.ErrorTopic, topic, "starlark on_event error: "+err.Error())
			}
		case <-time.After(env.ExecTimeout):
			publishError(client, env.ErrorTopic, topic, "starlark on_event timeout (best-effort)")
		}
	}

	client.Subscribe(env.SensorTopic, 0, handler)
	client.Subscribe(env.ChirpDeviceTopic, 0, handler)
	client.Subscribe(env.GatewayTopic, 0, handler)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("[actions] shutdown requested")
	client.Disconnect(250)
}

func buildEvent(topic string, payload []byte, sensorTopic, devTopic, gwTopic string) map[string]any {
	now := time.Now().UTC()
	ev := map[string]any{
		"ts":      now.Format(time.RFC3339Nano),
		"topic":   topic,
		"kind":    "unknown",
		"payload": string(payload),
	}

	if matchTopic(topic, sensorTopic) {
		ev["kind"] = "sensor"
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err == nil {
			ev["json"] = m
		} else {
			ev["json_error"] = err.Error()
		}
		return ev
	}

	if matchTopic(topic, devTopic) {
		ev["kind"] = "chirpstack_device"
		if d, err := csd.DecodeChirpStackDeviceEventJSON(topic, payload); err == nil {
			ev["json"] = d.Raw
		} else {
			ev["json_error"] = err.Error()
		}
		return ev
	}

	if matchTopic(topic, gwTopic) {
		ev["kind"] = "chirpstack_gateway"
		if g, err := csd.DecodeGatewayEventProtobuf(topic, payload); err == nil {
			ev["gateway"] = map[string]any{
				"region":     g.RegionHint,
				"gateway_id": g.GatewayID,
				"event_type": g.EventType,
				"raw_json":   g.RawJSON,
				"raw_b64":    g.RawBase64,
			}
		} else {
			ev["gateway_decode_error"] = err.Error()
			ev["payload_b64"] = base64.StdEncoding.EncodeToString(payload)
		}
		return ev
	}

	return ev
}

func publishError(client mqtt.Client, topic, srcTopic, message string) {
	payload := map[string]any{
		"ts":           time.Now().UTC().Format(time.RFC3339Nano),
		"source_topic": srcTopic,
		"error":        message,
	}
	b, _ := json.Marshal(payload)
	client.Publish(topic, 0, false, b)
}

type ScriptProgram struct {
	globals starlark.StringDict
	mu      sync.Mutex
}

func (p *ScriptProgram) CallOnEvent(event map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	fnv := p.globals["on_event"]
	fn, ok := fnv.(starlark.Callable)
	if !ok {
		return fmt.Errorf("script missing on_event(event)")
	}
	evVal := toStarlark(event)
	thread := &starlark.Thread{Name: "actions"}
	_, err := starlark.Call(thread, fn, starlark.Tuple{evVal}, nil)
	return err
}

type ScriptRunner struct {
	path     string
	lastMod  time.Time
	lastSize int64
	prog     *ScriptProgram
	mu       sync.Mutex
}

func NewScriptRunner(path string) *ScriptRunner { return &ScriptRunner{path: path} }

func (r *ScriptRunner) LoadIfChanged(builtins starlark.StringDict) (*ScriptProgram, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	st, err := os.Stat(r.path)
	if err != nil {
		return nil, err
	}
	if r.prog != nil && st.ModTime().Equal(r.lastMod) && st.Size() == r.lastSize {
		return r.prog, nil
	}

	src, err := os.ReadFile(r.path)
	if err != nil {
		return nil, err
	}

	abs := r.path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}

	thread := &starlark.Thread{Name: "load"}
	globals, err := starlark.ExecFile(thread, abs, src, builtins)
	if err != nil {
		return nil, err
	}

	r.prog = &ScriptProgram{globals: globals}
	r.lastMod = st.ModTime()
	r.lastSize = st.Size()

	fmt.Println("[actions] reloaded starlark script:", abs)
	return r.prog, nil
}

func toStarlark(v any) starlark.Value {
	switch x := v.(type) {
	case nil:
		return starlark.None
	case string:
		return starlark.String(x)
	case bool:
		if x {
			return starlark.True
		}
		return starlark.False
	case float64:
		return starlark.Float(x)
	case int:
		return starlark.MakeInt(x)
	case int64:
		return starlark.MakeInt64(x)
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
