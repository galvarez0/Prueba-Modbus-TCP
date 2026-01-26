package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

/* ===================== STRUCTS ===================== */

type ModbusHTTPPayload struct {
	SlaveID byte `json:"slave_id"`
	Request struct {
		Function byte     `json:"function_code"`
		Address  uint16   `json:"address"`
		Length   uint16   `json:"length"`
		Values   []uint16 `json:"values"`
	} `json:"request"`
}

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
	mqttClient mqtt.Client
)

/* ===================== MAIN ===================== */

func main() {
	fmt.Println("Servidor Modbus TCP MASTER iniciado")

	ctx, cancel := context.WithCancel(context.Background())

	initMQTT()
	go iniciarHTTP()
	go manejarShutdown(cancel)

	<-ctx.Done()
	fmt.Println("Shutdown completo")
}

/* ===================== MQTT ===================== */

func initMQTT() {
	opts := mqtt.NewClientOptions().
		AddBroker("tcp://localhost:1883").
		SetClientID("modbus-master")

	opts.OnConnect = func(c mqtt.Client) {
		fmt.Println("[MQTT] Conectado al broker")
		c.Subscribe("modbus/request", 0, manejarMQTTRequest)
	}

	mqttClient = mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
}

func manejarMQTTRequest(client mqtt.Client, msg mqtt.Message) {
	var payload ModbusHTTPPayload
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		fmt.Println("[MQTT] JSON inválido:", err)
		return
	}

	data, err := procesarModbus(payload)
	if err != nil {
		fmt.Println("[MQTT] Error:", err)
		return
	}

	mqttClient.Publish("modbus/response", 0, false, data)
}

/* ===================== HTTP ===================== */

func iniciarHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/connect", manejarConnect)
	mux.HandleFunc("/modbus", manejarHTTPModbus)
	mux.HandleFunc("/stats", manejarStats)

	httpServer = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println("HTTP escuchando en :8080")
	httpServer.ListenAndServe()
}

func manejarHTTPModbus(w http.ResponseWriter, r *http.Request) {
	var payload ModbusHTTPPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "json inválido", 400)
		return
	}

	data, err := procesarModbus(payload)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Write(data)
}

/* ===================== MODBUS CORE (FIXED) ===================== */

func procesarModbus(payload ModbusHTTPPayload) ([]byte, error) {
	mutex.Lock()
	slave, ok := slaves[payload.SlaveID]
	mutex.Unlock()

	if !ok || slave == nil {
		return nil, fmt.Errorf("slave %d no conectado", payload.SlaveID)
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
		return resp.Data, resp.Err
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout modbus")
	}
}

/* ===================== CONNECT ===================== */

func manejarConnect(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	port := r.URL.Query().Get("port")

	if id == "" || port == "" {
		http.Error(w, "faltan parámetros", 400)
		return
	}

	var slaveID byte
	fmt.Sscanf(id, "%d", &slaveID)

	conn, err := net.Dial("tcp", "127.0.0.1:"+port)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	slave := &Slave{
		ID:          slaveID,
		Conn:        conn,
		Queue:       make(chan ModbusRequest, 100),
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
	}

	mutex.Lock()
	slaves[slaveID] = slave
	mutex.Unlock()

	go loopSlave(slave)

	fmt.Printf("[SERVER] Slave %d conectado en %s\n", slaveID, conn.RemoteAddr())
	fmt.Fprintf(w, "Slave %d conectado\n", slaveID)
}

/* ===================== LOOP SLAVE ===================== */

func loopSlave(slave *Slave) {
	for req := range slave.Queue {

		adu := construirADU(slave, req)

		fmt.Printf("[TX] SLAVE %d:\n", slave.ID)
		printHex(adu)

		n, err := slave.Conn.Write(adu)
		if err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		slave.BytesTx += uint64(n)
		slave.Requests++

		mbap := make([]byte, 7)
		if _, err := io.ReadFull(slave.Conn, mbap); err != nil {
			req.Response <- ModbusResponse{Err: err}
			continue
		}

		length := int(binary.BigEndian.Uint16(mbap[4:6]))
		pdu := make([]byte, length-1)
		io.ReadFull(slave.Conn, pdu)

		resp := append(mbap, pdu...)

		slave.BytesRx += uint64(len(resp))
		slave.LastSeen = time.Now()

		fmt.Printf("[RX] SLAVE %d:\n", slave.ID)
		printHex(resp)

		req.Response <- ModbusResponse{Data: resp}
	}
}

func printHex(data []byte) {
	for _, b := range data {
		fmt.Printf("%02X ", b)
	}
	fmt.Println()
}

/* ===================== BUILD ADU ===================== */

func construirADU(slave *Slave, req ModbusRequest) []byte {
	slave.TransactionID++

	pdu := []byte{
		req.Function,
		byte(req.Address >> 8), byte(req.Address),
	}

	if req.Function == 0x03 {
		pdu = append(pdu, byte(req.Quantity>>8), byte(req.Quantity))
	}

	length := uint16(len(pdu) + 1)

	return append([]byte{
		byte(slave.TransactionID >> 8), byte(slave.TransactionID),
		0x00, 0x00,
		byte(length >> 8), byte(length),
		slave.ID,
	}, pdu...)
}

/* ===================== STATS ===================== */

func manejarStats(w http.ResponseWriter, r *http.Request) {
	mutex.Lock()
	defer mutex.Unlock()
	json.NewEncoder(w).Encode(slaves)
}

/* ===================== SHUTDOWN ===================== */

func manejarShutdown(cancel context.CancelFunc) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	<-ch
	fmt.Println("Shutdown solicitado")

	if mqttClient != nil {
		mqttClient.Disconnect(250)
	}

	cancel()
}