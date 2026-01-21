package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

/* ===================== ESTADOS ===================== */

type SlaveState string

const (
	StateIdle         SlaveState = "IDLE"
	StateBusy         SlaveState = "BUSY"
	StateTimeout      SlaveState = "TIMEOUT"
	StateDisconnected SlaveState = "DISCONNECTED"
)

/* ===================== ESTRUCTURAS ===================== */

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
	State         SlaveState
	LastActivity  time.Time
}

/* ===================== VARIABLES ===================== */

var (
	slaves = make(map[byte]*Slave)
	mutex  sync.Mutex
)

/* ===================== MAIN ===================== */

func main() {
	fmt.Println("Servidor Modbus TCP MASTER iniciado")

	go iniciarHTTP()
	go manejarShutdown()

	select {}
}

/* ===================== HTTP ===================== */

func iniciarHTTP() {
	http.HandleFunc("/connect", manejarConnect)
	http.HandleFunc("/modbus", manejarModbus)
	http.HandleFunc("/stats", manejarStats)

	fmt.Println("HTTP escuchando en :8080")
	http.ListenAndServe(":8080", nil)
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
		State:         StateIdle,
		LastActivity:  time.Now(),
	}

	mutex.Lock()
	slaves[slaveID] = slave
	mutex.Unlock()

	go loopSlave(slave)

	fmt.Printf("[SERVER] Slave %d conectado\n", slaveID)
	fmt.Fprintf(w, "Slave %d conectado\n", slaveID)
}

/* ===================== MODBUS HTTP ===================== */

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

		mutex.Lock()
		slave.State = StateBusy
		slave.LastActivity = time.Now()
		mutex.Unlock()

		adu := construirADU(slave, req)

		_, err := slave.Conn.Write(adu)
		if err != nil {
			setSlaveDisconnected(slave)
			req.Response <- ModbusResponse{Err: err}
			return
		}

		buffer := make([]byte, 256)
		slave.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := slave.Conn.Read(buffer)

		if err != nil {
			mutex.Lock()
			slave.State = StateTimeout
			slave.LastActivity = time.Now()
			mutex.Unlock()

			req.Response <- ModbusResponse{Err: err}
			continue
		}

		mutex.Lock()
		slave.State = StateIdle
		slave.LastActivity = time.Now()
		mutex.Unlock()

		req.Response <- ModbusResponse{Data: buffer[:n]}
	}
}

/* ===================== STATS ===================== */

func manejarStats(w http.ResponseWriter, r *http.Request) {
	type Stat struct {
		SlaveID      byte       `json:"slave_id"`
		Local        string     `json:"local"`
		Remote       string     `json:"remote"`
		State        SlaveState `json:"state"`
		QueueLen     int        `json:"queue_len"`
		LastActivity time.Time  `json:"last_activity"`
	}

	var resp []Stat

	mutex.Lock()
	for _, s := range slaves {
		resp = append(resp, Stat{
			SlaveID:      s.ID,
			Local:        s.Conn.LocalAddr().String(),
			Remote:       s.Conn.RemoteAddr().String(),
			State:        s.State,
			QueueLen:     len(s.Queue),
			LastActivity: s.LastActivity,
		})
	}
	mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

/* ===================== SHUTDOWN ===================== */

func manejarShutdown() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	<-ch
	fmt.Println("\n[SERVER] Shutdown iniciado")

	mutex.Lock()
	for _, s := range slaves {
		s.State = StateDisconnected
		s.Conn.Close()
		close(s.Queue)
	}
	mutex.Unlock()

	fmt.Println("[SERVER] Shutdown completo")
	os.Exit(0)
}

/* ===================== UTIL ===================== */

func setSlaveDisconnected(slave *Slave) {
	mutex.Lock()
	slave.State = StateDisconnected
	slave.LastActivity = time.Now()
	mutex.Unlock()
}

/* ===================== MODBUS BUILD ===================== */

func construirADU(slave *Slave, req ModbusRequest) []byte {
	slave.TransactionID++

	pdu := []byte{
		req.Function,
		byte(req.Address >> 8), byte(req.Address),
	}

	switch req.Function {
	case 0x03:
		pdu = append(pdu, byte(req.Quantity>>8), byte(req.Quantity))
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