package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"encoding/binary"
	"io"
)

/* ===================== STRUCTS ===================== */

type ModbusRequest struct {
	SlaveID  byte
	Function byte
	Address  uint16
	Quantity uint16
	Values   []uint16
	Response chan ModbusResponse
}

type ModbusResponse struct {
	Data []byte
	Err  error
}

type Slave struct {
	ID            byte
	Conn          net.Conn
	Queue         chan ModbusRequest
	TransactionID uint16

	ConnectedAt time.Time
	LastSeen    time.Time
	Requests    uint64
	BytesTx     uint64
	BytesRx     uint64
}

/* ===================== GLOBALS ===================== */

var (
	slaves = make(map[byte]*Slave)
	mutex  sync.Mutex

	httpServer *http.Server
)

/* ===================== MAIN ===================== */

func main() {
	fmt.Println("Servidor Modbus TCP MASTER iniciado")

	ctx, cancel := context.WithCancel(context.Background())

	go iniciarHTTP()
	go manejarShutdown(cancel)

	<-ctx.Done()
	fmt.Println("Shutdown completo")
}

/* ===================== HTTP ===================== */

func iniciarHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/connect", manejarConnect)
	mux.HandleFunc("/modbus", manejarModbus)
	mux.HandleFunc("/stats", manejarStats)
	mux.HandleFunc("/list_devices", manejarStats)

	httpServer = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println("HTTP escuchando en :8080")
	httpServer.ListenAndServe()
}

/* ===================== CONNECT ===================== */

func manejarConnect(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	port := r.URL.Query().Get("port")

	if id == "" || port == "" {
		http.Error(w, "faltan parametros id o port", 400)
		return
	}

	var slaveID byte
	fmt.Sscanf(id, "%d", &slaveID)

	address := "127.0.0.1:" + port
	conn, err := net.Dial("tcp", address)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	slave := &Slave{
		ID:            slaveID,
		Conn:          conn,
		Queue:         make(chan ModbusRequest, 1000),
		TransactionID: 1,
		ConnectedAt:   time.Now(),
		LastSeen:      time.Now(),
	}

	mutex.Lock()
	slaves[slaveID] = slave
	mutex.Unlock()

	go loopSlave(slave)

	fmt.Printf("[SERVER] Slave %d conectado en %s\n", slaveID, address)
	fmt.Fprintf(w, "Slave %d conectado\n", slaveID)
}

/* ===================== MODBUS ===================== */

func manejarModbus(w http.ResponseWriter, r *http.Request) {

	var payload struct {
		SlaveID byte `json:"slave_id"`
		Request struct {
			Function byte     `json:"function_code"`
			Address  uint16   `json:"address"`
			Length   uint16   `json:"length"`
			Values   []uint16 `json:"values"`
		} `json:"request"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "json invalido", 400)
		return
	}

	mutex.Lock()
	slave, ok := slaves[payload.SlaveID]
	mutex.Unlock()

	if !ok {
		http.Error(w, "slave no conectado", 404)
		return
	}

	req := ModbusRequest{
		SlaveID:  payload.SlaveID,
		Function: payload.Request.Function,
		Address:  payload.Request.Address,
		Quantity: payload.Request.Length,
		Values:   payload.Request.Values,
		Response: make(chan ModbusResponse),
	}

	slave.Queue <- req

	select {
	case resp := <-req.Response:
		if resp.Err != nil {
			http.Error(w, resp.Err.Error(), 500)
			return
		}
		w.Write(resp.Data)

	case <-time.After(5 * time.Second):
		http.Error(w, "timeout modbus", 504)
	}
}

/* ===================== LOOP SLAVE ===================== */

func loopSlave(slave *Slave) {
	for req := range slave.Queue {

		fmt.Printf("[QUEUE] dequeue SLAVE %d\n", slave.ID)

		adu := construirADU(slave, req)

		fmt.Printf("[TX] SLAVE %d:\n", slave.ID)
		for _, b := range adu {
			fmt.Printf("%02X ", b)
		}
		fmt.Println()

		if _, err := slave.Conn.Write(adu); err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		slave.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))

		/* ===== 1. Leer MBAP (7 bytes) ===== */
		mbap := make([]byte, 7)
		if _, err := io.ReadFull(slave.Conn, mbap); err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		length := int(binary.BigEndian.Uint16(mbap[4:6]))

		/* ===== 2. Leer PDU ===== */
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(slave.Conn, pdu); err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		resp := append(mbap, pdu...)

		fmt.Printf("[RX] SLAVE %d:\n", slave.ID)
		for _, b := range resp {
			fmt.Printf("%02X ", b)
		}
		fmt.Println()

		req.Response <- ModbusResponse{Data: resp}
	}
}


/* ===================== BUILD ADU ===================== */

func construirADU(slave *Slave, req ModbusRequest) []byte {
	slave.TransactionID++

	pdu := []byte{
		req.Function,
		byte(req.Address >> 8), byte(req.Address),
	}

	switch req.Function {
	case 0x03:
		pdu = append(pdu,
			byte(req.Quantity>>8), byte(req.Quantity),
		)
	case 0x10:
		qty := uint16(len(req.Values))
		pdu = append(pdu,
			byte(qty>>8), byte(qty),
			byte(qty*2),
		)
		for _, v := range req.Values {
			pdu = append(pdu, byte(v>>8), byte(v))
		}
	}

	length := uint16(1 + len(pdu))

	mbap := []byte{
		byte(slave.TransactionID >> 8), byte(slave.TransactionID),
		0x00, 0x00,
		byte(length >> 8), byte(length),
		slave.ID,
	}

	return append(mbap, pdu...)
}

/* ===================== STATS ===================== */

func manejarStats(w http.ResponseWriter, r *http.Request) {
	type stat struct {
		ID          byte      `json:"slave_id"`
		RemoteAddr  string    `json:"remote_addr"`
		ConnectedAt time.Time `json:"connected_at"`
		LastSeen    time.Time `json:"last_seen"`
		Requests    uint64    `json:"requests"`
		BytesTx     uint64    `json:"bytes_tx"`
		BytesRx     uint64    `json:"bytes_rx"`
		UptimeSec   int64     `json:"uptime_sec"`
	}

	mutex.Lock()
	defer mutex.Unlock()

	stats := []stat{}
	now := time.Now()

	for _, s := range slaves {
		stats = append(stats, stat{
			ID:          s.ID,
			RemoteAddr:  s.Conn.RemoteAddr().String(),
			ConnectedAt: s.ConnectedAt,
			LastSeen:    s.LastSeen,
			Requests:    s.Requests,
			BytesTx:     s.BytesTx,
			BytesRx:     s.BytesRx,
			UptimeSec:   int64(now.Sub(s.ConnectedAt).Seconds()),
		})
	}

	json.NewEncoder(w).Encode(stats)
}

/* ===================== SHUTDOWN ===================== */

func manejarShutdown(cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	<-ch
	fmt.Println("Shutdown solicitado")

	if httpServer != nil {
		httpServer.Shutdown(context.Background())
	}

	mutex.Lock()
	for _, s := range slaves {
		s.Conn.Close()
	}
	mutex.Unlock()

	cancel()
}