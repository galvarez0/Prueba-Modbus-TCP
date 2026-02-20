package chirpstackdecode

import (
    "encoding/base64"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    gw "github.com/chirpstack/chirpstack/api/go/v4/gw"
    "google.golang.org/protobuf/encoding/protojson"
    "google.golang.org/protobuf/proto"
)

type DeviceEventJSON struct {
    ReceivedAt    time.Time      `json:"received_at,omitempty"`
    Topic         string         `json:"topic"`
    EventType     string         `json:"event_type,omitempty"`
    DevEUI        string         `json:"dev_eui,omitempty"`
    ApplicationID string         `json:"application_id,omitempty"`
    DeviceName    string         `json:"device_name,omitempty"`
    FPort         *int           `json:"f_port,omitempty"`
    DataBase64    string         `json:"data_base64,omitempty"`
    Raw           map[string]any `json:"raw"`
}

// GatewayEventPB is a normalized representation of Gateway Bridge protobuf events.
type GatewayEventPB struct {
    Topic      string `json:"topic"`
    RegionHint string `json:"region_hint,omitempty"`
    EventType  string `json:"event_type,omitempty"`
    GatewayID  string `json:"gateway_id,omitempty"`
    RawJSON    string `json:"raw_json,omitempty"`
    RawBase64  string `json:"raw_b64,omitempty"`
}

func eventTypeFromTopic(topic string) string {
    parts := strings.Split(topic, "/")
    if len(parts) == 0 {
        return ""
    }
    return parts[len(parts)-1]
}

func parseTimeString(s string) (time.Time, bool) {
    if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
        return t, true
    }
    return time.Time{}, false
}

func DecodeChirpStackDeviceEventJSON(topic string, payload []byte) (*DeviceEventJSON, error) {
    var m map[string]any
    if err := json.Unmarshal(payload, &m); err != nil {
        return nil, fmt.Errorf("json unmarshal: %w", err)
    }

    out := &DeviceEventJSON{
        Topic:     topic,
        EventType: eventTypeFromTopic(topic),
        Raw:       m,
    }

    if v, ok := m["devEui"].(string); ok {
        out.DevEUI = v
    }
    if v, ok := m["applicationId"].(string); ok {
        out.ApplicationID = v
    }
    if v, ok := m["deviceName"].(string); ok {
        out.DeviceName = v
    }
    if di, ok := m["deviceInfo"].(map[string]any); ok {
        if v, ok := di["devEui"].(string); ok && out.DevEUI == "" {
            out.DevEUI = v
        }
        if v, ok := di["deviceName"].(string); ok && out.DeviceName == "" {
            out.DeviceName = v
        }
        if v, ok := di["applicationId"].(string); ok && out.ApplicationID == "" {
            out.ApplicationID = v
        }
    }

    if v, ok := m["fPort"].(float64); ok {
        iv := int(v)
        out.FPort = &iv
    }
    if v, ok := m["data"].(string); ok {
        out.DataBase64 = v
    }
    if v, ok := m["time"].(string); ok {
        if t, ok := parseTimeString(v); ok {
            out.ReceivedAt = t
        }
    }
    if v, ok := m["receivedAt"].(string); ok && out.ReceivedAt.IsZero() {
        if t, ok := parseTimeString(v); ok {
            out.ReceivedAt = t
        }
    }

    return out, nil
}

func DecodeGatewayEventProtobuf(topic string, payload []byte) (*GatewayEventPB, error) {
    parts := strings.Split(topic, "/")
    out := &GatewayEventPB{
        Topic:     topic,
        RawBase64: base64.StdEncoding.EncodeToString(payload),
        EventType: eventTypeFromTopic(topic),
    }
    if len(parts) >= 1 {
        out.RegionHint = parts[0]
    }
    if len(parts) >= 3 {
        out.GatewayID = parts[2]
    }

    candidates := []proto.Message{
        &gw.UplinkFrame{},
        &gw.DownlinkTxAck{},
        &gw.GatewayStats{},
        &gw.GatewayConfiguration{},
    }

    var lastErr error
    for _, msg := range candidates {
        if err := proto.Unmarshal(payload, msg); err != nil {
            lastErr = err
            continue
        }
        b, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(msg)
        if err != nil {
            lastErr = err
            continue
        }
        out.RawJSON = string(b)
        return out, nil
    }
    if lastErr != nil {
        return out, fmt.Errorf("protobuf decode failed: %w", lastErr)
    }
    return out, fmt.Errorf("protobuf decode failed")
}

func ParseChirpStackLogLine(line string) map[string]any {
    m := map[string]any{"line": line}
    fields := strings.Fields(line)
    if len(fields) > 0 {
        if t, ok := parseTimeString(fields[0]); ok {
            m["ts"] = t.Format(time.RFC3339Nano)
            if len(fields) > 1 {
                m["rest"] = strings.Join(fields[1:], " ")
            }
        }
    }
    return m
}
