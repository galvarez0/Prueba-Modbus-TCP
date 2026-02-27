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

/*
DB-backed Starlark routing (sensor scripts)
- For sensor events, pick script by sensor_id from Postgres table starlark_scripts.
- Fallback to sensor_id='default' if not found / disabled.
- Cache compiled programs with TTL (SCRIPT_CACHE_TTL).
- If TELEMETRY_POSTGRES_DSN is not provided, falls back to file-based STARLARK_SCRIPT.
*/

type Env struct {
	MQTTBroker string

	SensorTopic      string
	ChirpDeviceTopic string
	GatewayTopic     string

	// Fallback file script
	ScriptPath string

	// Optional sinks
	PostgresDSN   string
	ClickhouseDSN string
	ClickhouseDB  string
	ClickhouseTLS bool

	// Behavior
	MaxPayloadBytes int
	ExecTimeout     time.Duration
	ErrorTopic      string

	// DB scripts
	ScriptCacheTTL time.Duration
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

		ScriptCacheTTL: mustDuration(getenv("SCRIPT_CACHE_TTL", "10s"), 10*time.Second),
	}

	ctx := context.Background()

	// Optional Postgres (also used as the script store)
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

	// Optional ClickHouse sink
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

		// Best-effort table for raw events
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

	// MQTT
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

	// Builtins exposed to Starlark
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

	// Script engines:
	// 1) If Postgres is configured, use DB scripts per sensor_id with cache.
	// 2) Else, use file-based runner (existing behavior).
	var dbEngine *DBScriptEngine
	var fileRunner *FileScriptRunner

	if pgpool != nil {
		dbEngine = NewDBScriptEngine(pgpool, env.ScriptCacheTTL, builtins)
		fmt.Println("[actions] DB script engine enabled (telemetry postgres)")
	} else {
		fileRunner = NewFileScriptRunner(env.ScriptPath, builtins)
		fmt.Println("[actions] DB script engine disabled; using file:", env.ScriptPath)
	}

	handler := func(_ mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		payload := msg.Payload()
		if len(payload) > env.MaxPayloadBytes {
			publishError(client, env.ErrorTopic, topic, fmt.Sprintf("payload too large: %d bytes", len(payload)))
			return
		}

		event := buildEvent(topic, payload, env.SensorTopic, env.ChirpDeviceTopic, env.GatewayTopic)

		// Choose program
		var prog *ScriptProgram
		var err error

		if dbEngine != nil && event.Kind == "sensor" && event.SensorID != "" {
			prog, err = dbEngine.ProgramForSensor(ctx, event.SensorID)
		} else if fileRunner != nil {
			prog, err = fileRunner.LoadIfChanged()
		} else if dbEngine != nil {
			// Non-sensor events: use default
			prog, err = dbEngine.ProgramForSensor(ctx, "default")
		} else {
			err = fmt.Errorf("no script engine configured")
		}

		if err != nil {
			publishError(client, env.ErrorTopic, topic, "starlark load error: "+err.Error())
			return
		}

		done := make(chan error, 1)
		go func() { done <- prog.CallOnEvent(event.ToMap()) }()

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

/* ===================== Event model ===================== */

type Event struct {
	TS       string
	Kind     string
	Topic    string
	Payload  string
	JSON     map[string]any
	SensorID string

	// gateway decode
	Gateway map[string]any
}

func (e Event) ToMap() map[string]any {
	out := map[string]any{
		"ts":      e.TS,
		"kind":    e.Kind,
		"topic":   e.Topic,
		"payload": e.Payload,
	}
	if e.JSON != nil {
		out["json"] = e.JSON
	}
	if e.SensorID != "" {
		out["sensor_id"] = e.SensorID
	}
	if e.Gateway != nil {
		out["gateway"] = e.Gateway
	}
	return out
}

func buildEvent(topic string, payload []byte, sensorTopic, devTopic, gwTopic string) Event {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ev := Event{
		TS:      now,
		Topic:   topic,
		Kind:    "unknown",
		Payload: string(payload),
	}

	if matchTopic(topic, sensorTopic) {
		ev.Kind = "sensor"
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err == nil {
			ev.JSON = m
			if sid, ok := m["sensor_id"].(string); ok {
				ev.SensorID = sid
			}
		}
		return ev
	}

	if matchTopic(topic, devTopic) {
		ev.Kind = "chirpstack_device"
		if d, err := csd.DecodeChirpStackDeviceEventJSON(topic, payload); err == nil {
			ev.JSON = d.Raw
		}
		return ev
	}

	if matchTopic(topic, gwTopic) {
		ev.Kind = "chirpstack_gateway"
		if g, err := csd.DecodeGatewayEventProtobuf(topic, payload); err == nil {
			ev.Gateway = map[string]any{
				"region":     g.RegionHint,
				"gateway_id": g.GatewayID,
				"event_type": g.EventType,
				"raw_json":   g.RawJSON,
				"raw_b64":    g.RawBase64,
			}
		} else {
			ev.Gateway = map[string]any{
				"decode_error": err.Error(),
				"payload_b64":  base64.StdEncoding.EncodeToString(payload),
			}
		}
		return ev
	}

	return ev
}

/* ===================== Script program ===================== */

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

func compileProgram(filename string, src []byte, builtins starlark.StringDict) (*ScriptProgram, error) {
	thread := &starlark.Thread{Name: "load"}
	globals, err := starlark.ExecFile(thread, filename, src, builtins)
	if err != nil {
		return nil, err
	}
	return &ScriptProgram{globals: globals}, nil
}

/* ===================== DB script engine ===================== */

type cachedDBScript struct {
	sensorID  string
	script    string
	updatedAt time.Time

	fetchedAt time.Time
	prog      *ScriptProgram
}

type DBScriptEngine struct {
	pg       *pgxpool.Pool
	ttl      time.Duration
	builtins starlark.StringDict

	mu    sync.Mutex
	cache map[string]*cachedDBScript
}

func NewDBScriptEngine(pg *pgxpool.Pool, ttl time.Duration, builtins starlark.StringDict) *DBScriptEngine {
	return &DBScriptEngine{
		pg:       pg,
		ttl:      ttl,
		builtins: builtins,
		cache:    make(map[string]*cachedDBScript),
	}
}

func (e *DBScriptEngine) ProgramForSensor(ctx context.Context, sensorID string) (*ScriptProgram, error) {
	if strings.TrimSpace(sensorID) == "" {
		sensorID = "default"
	}

	// Check cache
	e.mu.Lock()
	c := e.cache[sensorID]
	if c != nil && time.Since(c.fetchedAt) < e.ttl && c.prog != nil {
		p := c.prog
		e.mu.Unlock()
		return p, nil
	}
	e.mu.Unlock()

	// Fetch from DB (sensor-specific, fallback to default)
	script, updatedAt, err := e.fetchScript(ctx, sensorID)
	if err != nil {
		return nil, err
	}

	// Compile
	filename := fmt.Sprintf("db:%s", sensorID)
	prog, err := compileProgram(filename, []byte(script), e.builtins)
	if err != nil {
		return nil, err
	}

	// Store cache
	e.mu.Lock()
	e.cache[sensorID] = &cachedDBScript{
		sensorID:  sensorID,
		script:    script,
		updatedAt: updatedAt,
		fetchedAt: time.Now(),
		prog:      prog,
	}
	e.mu.Unlock()

	return prog, nil
}

func (e *DBScriptEngine) fetchScript(ctx context.Context, sensorID string) (string, time.Time, error) {
	type row struct {
		script    string
		updatedAt time.Time
	}

	// Sensor-specific first
	var r row
	err := e.pg.QueryRow(ctx,
		`SELECT script, updated_at
         FROM starlark_scripts
         WHERE sensor_id=$1 AND enabled=true
         ORDER BY updated_at DESC
         LIMIT 1`,
		sensorID,
	).Scan(&r.script, &r.updatedAt)

	if err == nil && strings.TrimSpace(r.script) != "" {
		return r.script, r.updatedAt, nil
	}

	// Fallback default
	err = e.pg.QueryRow(ctx,
		`SELECT script, updated_at
         FROM starlark_scripts
         WHERE sensor_id='default' AND enabled=true
         ORDER BY updated_at DESC
         LIMIT 1`,
	).Scan(&r.script, &r.updatedAt)

	if err != nil {
		return "", time.Time{}, fmt.Errorf("no starlark script found for sensor_id=%q and no default script", sensorID)
	}
	if strings.TrimSpace(r.script) == "" {
		return "", time.Time{}, fmt.Errorf("default starlark script is empty")
	}
	return r.script, r.updatedAt, nil
}

/* ===================== File fallback runner ===================== */

type FileScriptRunner struct {
	path     string
	builtins starlark.StringDict

	lastMod  time.Time
	lastSize int64
	prog     *ScriptProgram
	mu       sync.Mutex
}

func NewFileScriptRunner(path string, builtins starlark.StringDict) *FileScriptRunner {
	return &FileScriptRunner{path: path, builtins: builtins}
}

func (r *FileScriptRunner) LoadIfChanged() (*ScriptProgram, error) {
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

	prog, err := compileProgram(abs, src, r.builtins)
	if err != nil {
		return nil, err
	}

	r.prog = prog
	r.lastMod = st.ModTime()
	r.lastSize = st.Size()

	fmt.Println("[actions] reloaded file starlark script:", abs)
	return r.prog, nil
}

/* ===================== Helpers ===================== */

func publishError(client mqtt.Client, topic, srcTopic, message string) {
	payload := map[string]any{
		"ts":           time.Now().UTC().Format(time.RFC3339Nano),
		"source_topic": srcTopic,
		"error":        message,
	}
	b, _ := json.Marshal(payload)
	client.Publish(topic, 0, false, b)
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
