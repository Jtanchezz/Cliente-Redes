package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxPacketSize = 1024
	ackTimeout    = 5 * time.Second
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type User struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type UDPMessage struct {
	Server     string `json:"server"`
	From       string `json:"from"`
	To         string `json:"to"`
	Type       string `json:"type"`
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	ClientAddr string `json:"client_addr,omitempty"`
	Text       string `json:"text,omitempty"`
	RefID      string `json:"ref_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Code       string `json:"code,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Users      []User `json:"users,omitempty"`
}

type SSEBroker struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewSSEBroker() *SSEBroker {
	return &SSEBroker{clients: make(map[chan []byte]struct{})}
}

func (b *SSEBroker) Subscribe() chan []byte {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *SSEBroker) Unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

func (b *SSEBroker) Publish(event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\n" + "data: " + string(data) + "\n\n")
	b.mu.Lock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	b.mu.Unlock()
}

type App struct {
	mu          sync.Mutex
	currentUser string
	serverHost  string
	serverPort  int
	serverAddr  *net.UDPAddr
	udpConn     *net.UDPConn
	udpStarted  bool
	pendingAcks map[string]time.Time
	usersCache  []User
	broker      *SSEBroker
}

func NewApp() *App {
	return &App{
		pendingAcks: make(map[string]time.Time),
		usersCache:  []User{},
		broker:      NewSSEBroker(),
	}
}

type configRequest struct {
	User       string `json:"user"`
	ServerHost string `json:"serverHost"`
	ServerPort int    `json:"serverPort"`
}

type sendRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var req configRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	user, err := normalizeName(req.User)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ServerHost == "" {
		writeError(w, http.StatusBadRequest, "serverHost is required")
		return
	}
	port := req.ServerPort
	if port == 0 {
		port = 9999
	}
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", req.ServerHost, port))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server address")
		return
	}

	a.mu.Lock()
	a.currentUser = user
	a.serverHost = req.ServerHost
	a.serverPort = port
	a.serverAddr = addr
	if a.udpConn == nil {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			a.mu.Unlock()
			writeError(w, http.StatusInternalServerError, "unable to open udp socket")
			return
		}
		a.udpConn = conn
	}
	started := a.udpStarted
	if !a.udpStarted {
		a.udpStarted = true
	}
	a.mu.Unlock()

	if !started {
		go a.udpReadLoop()
		go a.ackTimeoutLoop()
	}

	if err := a.sendJoin(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	a.broker.Publish("status", map[string]interface{}{
		"connected": true,
		"user":      user,
		"server":    fmt.Sprintf("%s:%d", req.ServerHost, port),
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"user":       user,
		"serverHost": req.ServerHost,
		"serverPort": port,
	})
}

func (a *App) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var req sendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	to, err := normalizeName(req.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || len(text) > 512 {
		writeError(w, http.StatusBadRequest, "text must be 1-512 characters")
		return
	}

	from, serverHost, serverAddr, conn := a.getConnState()
	if from == "" || serverHost == "" || serverAddr == nil || conn == nil {
		writeError(w, http.StatusBadRequest, "client not configured")
		return
	}

	id := newUUID()
	msg := UDPMessage{
		Server: serverHost,
		From:   from,
		To:     to,
		Type:   "MSG",
		ID:     id,
		Text:   text,
	}
	if err := a.sendUDP(msg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	a.mu.Lock()
	a.pendingAcks[id] = time.Now()
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	from, serverHost, _, _ := a.getConnState()
	if from == "" || serverHost == "" {
		writeError(w, http.StatusBadRequest, "client not configured")
		return
	}
	msg := UDPMessage{
		Server: serverHost,
		From:   from,
		To:     "server",
		Type:   "LIST",
		ID:     newUUID(),
	}
	if err := a.sendUDP(msg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := a.broker.Subscribe()
	defer a.broker.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			_, _ = w.Write(msg)
			flusher.Flush()
		}
	}
}

func (a *App) sendJoin() error {
	from, serverHost, _, _ := a.getConnState()
	if from == "" || serverHost == "" {
		return errors.New("client not configured")
	}
	msg := UDPMessage{
		Server: serverHost,
		From:   from,
		To:     "server",
		Type:   "JOIN",
		ID:     newUUID(),
		Name:   from,
	}
	return a.sendUDP(msg)
}

func (a *App) getConnState() (string, string, *net.UDPAddr, *net.UDPConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentUser, a.serverHost, a.serverAddr, a.udpConn
}

func (a *App) sendUDP(msg UDPMessage) error {
	_, serverHost, serverAddr, conn := a.getConnState()
	if serverAddr == nil || conn == nil || serverHost == "" {
		return errors.New("client not configured")
	}
	if msg.Server == "" {
		msg.Server = serverHost
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.New("unable to encode message")
	}
	if len(data) > maxPacketSize {
		return errors.New("message exceeds 1024 bytes")
	}
	_, err = conn.WriteToUDP(data, serverAddr)
	if err != nil {
		return errors.New("udp send failed")
	}
	return nil
}

func (a *App) udpReadLoop() {
	buf := make([]byte, maxPacketSize)
	for {
		conn := func() *net.UDPConn {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.udpConn
		}()
		if conn == nil {
			return
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			a.broker.Publish("error", map[string]string{
				"code":   "UDP_READ",
				"detail": "udp read failed",
			})
			continue
		}
		if n <= 0 {
			continue
		}
		if n > maxPacketSize {
			a.broker.Publish("error", map[string]string{
				"code":   "TOO_LONG",
				"detail": "message exceeds 1024 bytes",
			})
			continue
		}
		var msg UDPMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			a.broker.Publish("error", map[string]string{
				"code":   "BAD_FORMAT",
				"detail": "invalid json from server",
			})
			continue
		}

		switch strings.ToUpper(msg.Type) {
		case "MSG":
			a.handleIncomingMessage(msg)
		case "ACK":
			a.handleAck(msg)
		case "USERS":
			a.handleUsers(msg)
		case "ERR":
			a.handleErr(msg)
		default:
			a.broker.Publish("error", map[string]string{
				"code":   "UNKNOWN_TYPE",
				"detail": "unknown message type",
			})
		}
	}
}

func (a *App) handleIncomingMessage(msg UDPMessage) {
	from, serverHost, _, _ := a.getConnState()
	if from == "" || serverHost == "" {
		return
	}
	if msg.To != "" && !strings.EqualFold(msg.To, from) {
		return
	}
	a.broker.Publish("message", map[string]interface{}{
		"id":        msg.ID,
		"from":      msg.From,
		"to":        msg.To,
		"text":      msg.Text,
		"direction": "in",
		"at":        time.Now().Format(time.RFC3339),
	})
	if msg.ID == "" {
		return
	}

	ack := UDPMessage{
		Server: serverHost,
		From:   from,
		To:     "server",
		Type:   "ACK",
		ID:     newUUID(),
		RefID:  msg.ID,
		Status: "received",
	}
	_ = a.sendUDP(ack)
}

func (a *App) handleAck(msg UDPMessage) {
	ref := msg.RefID
	if ref == "" {
		return
	}
	a.mu.Lock()
	delete(a.pendingAcks, ref)
	a.mu.Unlock()

	status := msg.Status
	if status == "" {
		status = "received"
	}
	a.broker.Publish("ack", map[string]string{
		"ref_id": ref,
		"status": status,
	})
}

func (a *App) handleUsers(msg UDPMessage) {
	a.mu.Lock()
	a.usersCache = msg.Users
	a.mu.Unlock()
	a.broker.Publish("users", map[string]interface{}{
		"users": msg.Users,
		"at":    time.Now().Format(time.RFC3339),
	})
}

func (a *App) handleErr(msg UDPMessage) {
	a.broker.Publish("error", map[string]string{
		"code":   msg.Code,
		"detail": msg.Detail,
		"ref_id": msg.RefID,
	})
}

func (a *App) ackTimeoutLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		var expired []string
		now := time.Now()
		a.mu.Lock()
		for id, ts := range a.pendingAcks {
			if now.Sub(ts) > ackTimeout {
				expired = append(expired, id)
				delete(a.pendingAcks, id)
			}
		}
		a.mu.Unlock()
		for _, id := range expired {
			a.broker.Publish("ack", map[string]string{
				"ref_id": id,
				"status": "timeout",
			})
		}
	}
}

func normalizeName(input string) (string, error) {
	name := strings.TrimSpace(input)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len(name) < 1 || len(name) > 20 {
		return "", errors.New("name must be 1-20 characters")
	}
	if !usernameRe.MatchString(name) {
		return "", errors.New("name must be alphanumeric with _ or -")
	}
	return strings.ToLower(name), nil
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	node := uint64(0)
	for i := 10; i < 16; i++ {
		node = (node << 8) | uint64(b[i])
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		node,
	)
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type spaHandler struct {
	publicDir string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "" {
		h.serveFileOrIndex(w, r, "index.html")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	cleanPath := filepath.Clean(r.URL.Path)
	cleanPath = strings.TrimPrefix(cleanPath, string(filepath.Separator))
	path := filepath.Join(h.publicDir, cleanPath)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	h.serveFileOrIndex(w, r, "index.html")
}

func (h spaHandler) serveFileOrIndex(w http.ResponseWriter, r *http.Request, filename string) {
	path := filepath.Join(h.publicDir, filename)
	if _, err := os.Stat(path); err == nil {
		http.ServeFile(w, r, path)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Cliente UDP</title>
  <style>
    body { font-family: Arial, sans-serif; padding: 2rem; }
    code { background: #f4f4f4; padding: 0.2rem 0.4rem; }
  </style>
</head>
<body>
  <h1>UI no encontrada</h1>
  <p>Construye la interfaz con Vite:</p>
  <ol>
    <li><code>cd frontend</code></li>
    <li><code>npm install</code></li>
    <li><code>npm run build</code></li>
  </ol>
  <p>Luego reinicia el backend Go.</p>
</body>
</html>`))
}

func main() {
	httpAddr := getenv("HTTP_ADDR", ":8080")
	publicDir := getenv("PUBLIC_DIR", "frontend/dist")

	app := NewApp()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/send", app.handleSend)
	mux.HandleFunc("/api/list", app.handleList)
	mux.HandleFunc("/api/events", app.handleEvents)
	mux.Handle("/", spaHandler{publicDir: publicDir})

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("HTTP listening on %s", httpAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
