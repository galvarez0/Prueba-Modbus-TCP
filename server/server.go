package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/galvarez0/Prueba-Modbus-TCP/server/internal/modbus"
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
	broker := os.Getenv("MQTT_BROKER")
	if broker == "" {
		broker = "tcp://localhost:1883"
	}

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID("modbus-master").
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(3 * time.Second)

	opts.OnConnect = func(c mqtt.Client) {
		fmt.Println("[MQTT] Conectado al broker", broker)
		c.Subscribe("modbus/request", 0, manejarMQTTRequest)
	}

	opts.OnConnectionLost = func(_ mqtt.Client, err error) {
		fmt.Println("[MQTT] conexión perdida:", err)
	}

	mqttClient = mqtt.NewClient(opts)

	go func() {
		for {
			token := mqttClient.Connect()
			token.Wait()
			if token.Error() == nil {
				return
			}
			fmt.Println("[MQTT] retry:", token.Error())
			time.Sleep(3 * time.Second)
		}
	}()
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

	// productores por curl
	mux.HandleFunc("/test", manejarTest)
	mux.HandleFunc("/read", manejarRead)
	mux.HandleFunc("/write", manejarWrite)

	httpServer = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println("HTTP escuchando en :8080")
	_ = httpServer.ListenAndServe()
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
		Response: make(chan ModbusResponse, 1), // buffered para evitar deadlock
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
	host := r.URL.Query().Get("host")
	port := r.URL.Query().Get("port")

	if id == "" || port == "" {
		http.Error(w, "faltan parámetros", 400)
		return
	}
	if host == "" {
		host = "127.0.0.1"
	}

	var slaveID byte
	fmt.Sscanf(id, "%d", &slaveID)

	// evita duplicados
	mutex.Lock()
	if _, ok := slaves[slaveID]; ok {
		mutex.Unlock()
		http.Error(w, "slave ya conectado", 409)
		return
	}
	mutex.Unlock()

	conn, err := net.Dial("tcp", host+":"+port)
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

// /test?id=1  -> read addr=0 qty=1
func manejarTest(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "faltan parámetros", 400)
		return
	}
	r.URL.RawQuery = "id=" + id + "&addr=0&qty=1"
	manejarRead(w, r)
}

// /read?id=1&addr=0&qty=1
func manejarRead(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	addrStr := r.URL.Query().Get("addr")
	qtyStr := r.URL.Query().Get("qty")

	if idStr == "" || addrStr == "" || qtyStr == "" {
		http.Error(w, "faltan parámetros", 400)
		return
	}

	idInt, err := strconv.Atoi(idStr)
	if err != nil || idInt < 0 || idInt > 255 {
		http.Error(w, "id inválido", 400)
		return
	}
	addr, err := parseUint16(addrStr)
	if err != nil {
		http.Error(w, "addr inválido", 400)
		return
	}
	qty, err := parseUint16(qtyStr)
	if err != nil || qty == 0 {
		http.Error(w, "qty inválido", 400)
		return
	}

	payload := ModbusHTTPPayload{SlaveID: byte(idInt)}
	payload.Request.Function = 0x03
	payload.Request.Address = addr
	payload.Request.Length = qty

	data, err := procesarModbus(payload)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, hex.EncodeToString(data))
}

func manejarWrite(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	addrStr := r.URL.Query().Get("addr")
	valuesStr := r.URL.Query().Get("values")

	if idStr == "" || addrStr == "" || valuesStr == "" {
		http.Error(w, "faltan parámetros", 400)
		return
	}

	idInt, err := strconv.Atoi(idStr)
	if err != nil || idInt < 0 || idInt > 255 {
		http.Error(w, "id inválido", 400)
		return
	}
	addr, err := parseUint16(addrStr)
	if err != nil {
		http.Error(w, "addr inválido", 400)
		return
	}

	values, err := parseCSVUint16(valuesStr)
	if err != nil || len(values) == 0 {
		http.Error(w, "values inválido", 400)
		return
	}

	payload := ModbusHTTPPayload{SlaveID: byte(idInt)}
	payload.Request.Function = 0x10
	payload.Request.Address = addr
	payload.Request.Length = uint16(len(values))
	payload.Request.Values = values

	data, err := procesarModbus(payload)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, hex.EncodeToString(data))
}

func parseUint16(s string) (uint16, error) {
	// soporta decimal "10" o hex "0x0A"
	base := 10
	ss := strings.TrimSpace(s)
	if strings.HasPrefix(ss, "0x") || strings.HasPrefix(ss, "0X") {
		base = 16
		ss = ss[2:]
	}
	u, err := strconv.ParseUint(ss, base, 16)
	if err != nil {
		return 0, err
	}
	return uint16(u), nil
}

func parseCSVUint16(s string) ([]uint16, error) {
	parts := strings.Split(s, ",")
	out := make([]uint16, 0, len(parts))
	for _, p := range parts {
		v, err := parseUint16(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

/* ===================== LOOP SLAVE ===================== */

func loopSlave(slave *Slave) {
	defer func() {
		fmt.Printf("[SERVER] Slave %d desconectado\n", slave.ID)

		mutex.Lock()
		delete(slaves, slave.ID)
		mutex.Unlock()

		_ = slave.Conn.Close()
	}()

	for req := range slave.Queue {

		adu := construirADU(slave, req)

		fmt.Printf("[TX] SLAVE %d:\n", slave.ID)
		printHex(adu)

		n, err := slave.Conn.Write(adu)
		if err != nil {
			nonBlockingReply(req.Response, ModbusResponse{Err: err})
			continue
		}

		slave.BytesTx += uint64(n)
		slave.Requests++

		mbap := make([]byte, 7)
		if _, err := io.ReadFull(slave.Conn, mbap); err != nil {
			nonBlockingReply(req.Response, ModbusResponse{Err: err})
			continue
		}

		length := modbus.GetUint16(mbap[4:6])
		if length < 2 {
			nonBlockingReply(req.Response, ModbusResponse{Err: fmt.Errorf("respuesta modbus inválida (length=%d)", length)})
			continue
		}

		pdu := make([]byte, length-1)

		if _, err := io.ReadFull(slave.Conn, pdu); err != nil {
			nonBlockingReply(req.Response, ModbusResponse{Err: err})
			continue
		}

		resp := append(mbap, pdu...)

		slave.BytesRx += uint64(len(resp))
		slave.LastSeen = time.Now()

		fmt.Printf("[RX] SLAVE %d:\n", slave.ID)
		printHex(resp)

		nonBlockingReply(req.Response, ModbusResponse{Data: resp})
	}
}

func nonBlockingReply(ch chan ModbusResponse, resp ModbusResponse) {
	if ch == nil {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func printHex(data []byte) {
	for _, b := range data {
		fmt.Printf("%02X ", b)
	}
	fmt.Println()
}

func construirADU(slave *Slave, req ModbusRequest) []byte {
	slave.TransactionID++

	pdu := []byte{req.Function}

	addr := make([]byte, 2)
	modbus.PutUint16(addr, req.Address)
	pdu = append(pdu, addr...)

	switch req.Function {
	case 0x03:
		// quantity
		q := make([]byte, 2)
		modbus.PutUint16(q, req.Quantity)
		pdu = append(pdu, q...)

	case 0x10:
		// quantity
		q := make([]byte, 2)
		modbus.PutUint16(q, req.Quantity)
		pdu = append(pdu, q...)

		byteCount := byte(len(req.Values) * 2)
		pdu = append(pdu, byteCount)

		for _, v := range req.Values {
			tmp := make([]byte, 2)
			modbus.PutUint16(tmp, v)
			pdu = append(pdu, tmp...)
		}
	}

	length := uint16(len(pdu) + 1)

	mbap := make([]byte, 7)
	modbus.PutUint16(mbap[0:2], slave.TransactionID)
	mbap[2], mbap[3] = 0x00, 0x00
	modbus.PutUint16(mbap[4:6], length)
	mbap[6] = slave.ID

	return append(mbap, pdu...)
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
