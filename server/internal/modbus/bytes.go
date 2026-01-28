package modbus

func PutUint16(b []byte, v uint16) {
	b[0] = byte(v >> 8)
	b[1] = byte(v & 0xFF)
}

func GetUint16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}
