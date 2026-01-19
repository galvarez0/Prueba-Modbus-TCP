package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

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
}

var (
	slaves = make(map[byte]*Slave)
	mutex  sync.Mutex
)

func main() {
	fmt.Println("Servidor Modbus TCP MASTER iniciado")

	go iniciarHTTP()

	select {}
}

func iniciarHTTP() {
	http.HandleFunc("/connect", manejarConnect)
	http.HandleFunc("/modbus", manejarModbus)

	fmt.Println("HTTP escuchando en :8080")
	http.ListenAndServe(":8080", nil)
}

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
	}

	mutex.Lock()
	slaves[slaveID] = slave
	mutex.Unlock()

	go loopSlave(slave)

	fmt.Fprintf(w, "Slave %d conectado\n", slaveID)
}

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

	fmt.Printf("[SLAVE %d] enqueue request\n", payload.SlaveID)
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

func loopSlave(slave *Slave) {
	for req := range slave.Queue {
		fmt.Printf("[SLAVE %d] dequeue request\n", slave.ID)

		adu := construirADU(slave, req)
		_, err := slave.Conn.Write(adu)
		if err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		buffer := make([]byte, 256)
		slave.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := slave.Conn.Read(buffer)
		if err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		fmt.Printf("[SLAVE %d] response received\n", slave.ID)
		req.Response <- ModbusResponse{Data: buffer[:n]}
	}
}

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