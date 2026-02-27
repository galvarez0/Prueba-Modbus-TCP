\
package chirpstackdecode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	gwb "github.com/chirpstack/chirpstack/api/go/v4/gw"
	"google.golang.org/protobuf/proto"
)

type GatewayEvent struct {
	Topic      string
	RegionHint string
	GatewayID  string
	EventType  string
	RawJSON    string
	RawBase64  string
}

func DecodeGatewayEventProtobuf(topic string, payload []byte) (*GatewayEvent, error) {
	// Expected by gateway-bridge config:
	// <region>/gateway/<gatewayId>/event/<EventType>
	parts := strings.Split(topic, "/")
	if len(parts) < 5 {
		return nil, fmt.Errorf("unexpected gateway topic: %s", topic)
	}
	region := parts[0]
	gwID := parts[2]
	evType := parts[4]

	var asJSON string

	// Try to decode as one of the known protobuf messages; fall back to base64 only.
	// Common event types include: up, stats, ack, exec, raw
	switch strings.ToLower(evType) {
	case "up":
		var m gwb.UplinkFrame
		if err := proto.Unmarshal(payload, &m); err == nil {
			b, _ := json.Marshal(m)
			asJSON = string(b)
		}
	case "stats":
		var m gwb.GatewayStats
		if err := proto.Unmarshal(payload, &m); err == nil {
			b, _ := json.Marshal(m)
			asJSON = string(b)
		}
	default:
		// unknown type; just keep empty json
	}

	return &GatewayEvent{
		Topic:      topic,
		RegionHint: region,
		GatewayID:  gwID,
		EventType:  evType,
		RawJSON:    asJSON,
		RawBase64:  base64.StdEncoding.EncodeToString(payload),
	}, nil
}
