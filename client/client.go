package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const basePort = 5020
const registerCount = 100

func main() {

	if len(os.Args) < 2 {
		fmt.Println("Uso: ./client <slave_id>")
		return
	}

	slaveIDInt, _ := strconv.Atoi(os.Args[1])
	slaveID := byte(slaveIDInt)

	port := basePort + slaveIDInt
	address := fmt.Sprintf(":%d", port)

	fmt.Printf("Cliente Modbus TCP iniciado (SLAVE %d) en puerto %d\n", slaveID, port)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Println("Error al iniciar listener:", err)
		return
	}
	defer listener.Close()

	holding := make([]uint16, registerCount)
	for i := range holding {
		holding[i] = uint16(i)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error al aceptar conexión:", err)
			continue
		}

		fmt.Printf("Nueva conexión aceptada (SLAVE %d)\n", slaveID)
		go manejarConexion(conn, slaveID, holding)
	}
}

func manejarConexion(conn net.Conn, slaveID byte, holding []uint16) {
	defer conn.Close()

	buffer := make([]byte, 260)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Printf("SLAVE %d desconectado\n", slaveID)
			return
		}

		fmt.Printf("SLAVE %d request recibido:\n", slaveID)
		imprimirHex(buffer[:n])

		transactionID := buffer[0:2]
		unitID := buffer[6]
		function := buffer[7]

		var pduResp []byte

		switch function {

		case 0x03:
			address := uint16(buffer[8])<<8 | uint16(buffer[9])
			quantity := uint16(buffer[10])<<8 | uint16(buffer[11])

			byteCount := byte(quantity * 2)
			pduResp = append([]byte{0x03, byteCount})

			for i := uint16(0); i < quantity; i++ {
				val := holding[address+i]
				pduResp = append(pduResp, byte(val>>8), byte(val))
			}

		case 0x10:
			address := uint16(buffer[8])<<8 | uint16(buffer[9])
			quantity := uint16(buffer[10])<<8 | uint16(buffer[11])

			valuesStart := 13
			for i := uint16(0); i < quantity; i++ {
				val := uint16(buffer[valuesStart])<<8 | uint16(buffer[valuesStart+1])
				holding[address+i] = val
				valuesStart += 2
			}

			pduResp = []byte{
				0x10,
				byte(address >> 8), byte(address),
				byte(quantity >> 8), byte(quantity),
			}

		default:
			fmt.Printf("SLAVE %d función no soportada: %02X\n", slaveID, function)
			return
		}

		length := uint16(len(pduResp) + 1)

		response := []byte{
			transactionID[0], transactionID[1],
			0x00, 0x00,
			byte(length >> 8), byte(length),
			unitID,
		}

		response = append(response, pduResp...)

		conn.Write(response)

		fmt.Printf("SLAVE %d response enviada:\n", slaveID)
		imprimirHex(response)
	}
}

func imprimirHex(data []byte) {
	for _, b := range data {
		fmt.Printf("%02X ", b)
	}
	fmt.Println()
}
