\
package chirpstackdecode

import (
	"encoding/json"
	"fmt"
	"strings"
)

type DeviceEvent struct {
	Topic         string
	ApplicationID string
	DevEUI        string
	EventType     string
	Raw           map[string]any
}

func DecodeChirpStackDeviceEventJSON(topic string, payload []byte) (*DeviceEvent, error) {
	// Expected: application/<appId>/device/<devEui>/event/<eventType>
	parts := strings.Split(topic, "/")
	if len(parts) < 6 {
		return nil, fmt.Errorf("unexpected chirpstack device topic: %s", topic)
	}
	ev := &DeviceEvent{
		Topic:         topic,
		ApplicationID: parts[1],
		DevEUI:        parts[3],
		EventType:     parts[5],
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, err
	}
	ev.Raw = m
	return ev, nil
}
