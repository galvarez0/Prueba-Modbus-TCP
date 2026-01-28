package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"github.com/galvarez0/Prueba-Modbus-TCP/client/internal/modbus"
)

const (
	listenPort    = 5021
	registerCount = 100
)

/* ===================== STRUCTS ===================== */

type request struct {
	conn net.Conn
	data []byte
}

/* ===================== GLOBALS ===================== */

var (
	shutdownOnce sync.Once
	reqQueue     = make(chan request, 100)
)

/* ===================== MAIN ===================== */

func main() {

	if len(os.Args) < 2 {
		fmt.Println("Uso: ./client <slave_id>")
		return
	}

	slaveIDInt, _ := strconv.Atoi(os.Args[1])
	slaveID := byte(slaveIDInt)

	address := fmt.Sprintf(":%d", listenPort)

	fmt.Printf(
		"Cliente Modbus TCP iniciado (SLAVE %d) escuchando en puerto %d\n",
		slaveID, listenPort,
	)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Println("Error al iniciar listener:", err)
		return
	}

	holding := make([]uint16, registerCount)
	for i := range holding {
		holding[i] = uint16(i)
	}

	// Worker único (serializa Modbus)
	go procesarRequests(slaveID, holding)

	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(0)
		}

		fmt.Printf("MASTER conectado (SLAVE %d)\n", slaveID)
		go leerSocket(conn, listener)
	}
}

/* ===================== SOCKET READ ===================== */

func leerSocket(conn net.Conn, listener net.Listener) {
	defer conn.Close()

	buffer := make([]byte, 260)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println("MASTER desconectado")
			terminarProceso(listener)
			return
		}

		// Copia segura del frame
		frame := make([]byte, n)
		copy(frame, buffer[:n])

		reqQueue <- request{
			conn: conn,
			data: frame,
		}
	}
}

/* ===================== MODBUS WORKER ===================== */

func procesarRequests(slaveID byte, holding []uint16) {
	for req := range reqQueue {

		buffer := req.data
		conn := req.conn

		if len(buffer) < 8 {
			continue
		}

		transactionID := buffer[0:2]
		unitID := buffer[6]
		function := buffer[7]

		var pduResp []byte

		switch function {

		case 0x03:
			address := modbus.GetUint16(buffer[8:10])
			quantity := modbus.GetUint16(buffer[10:12])

			if int(address+quantity) > len(holding) {
				pduResp = []byte{function | 0x80, 0x02}
				break
			}

			byteCount := byte(quantity * 2)
			pduResp = []byte{0x03, byteCount}

			for i := uint16(0); i < quantity; i++ {
				tmp := make([]byte, 2)
				modbus.PutUint16(tmp, holding[address+i])
				pduResp = append(pduResp, tmp...)
			}

		case 0x10:
			address := modbus.GetUint16(buffer[8:10])
			quantity := modbus.GetUint16(buffer[10:12])

			valuesStart := 13
			for i := uint16(0); i < quantity; i++ {
				val := modbus.GetUint16(buffer[valuesStart : valuesStart+2])
				holding[address+i] = val
				valuesStart += 2
			}

			pduResp = []byte{
				0x10,
				byte(address >> 8), byte(address),
				byte(quantity >> 8), byte(quantity),
			}

		default:
			pduResp = []byte{function | 0x80, 0x01}
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
	}
}

/* ===================== SHUTDOWN ===================== */

func terminarProceso(listener net.Listener) {
	shutdownOnce.Do(func() {
		fmt.Println("SLAVE terminando proceso")
		listener.Close()
		os.Exit(0)
	})
}
