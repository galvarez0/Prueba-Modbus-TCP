package modbus

import "testing"

func TestUint16Roundtrip(t *testing.T) {
	buf := make([]byte, 2)
	var v uint16 = 0xBEEF

	PutUint16(buf, v)
	if GetUint16(buf) != v {
		t.Fatal("uint16 conversion failed")
	}
}
