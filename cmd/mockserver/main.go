package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxPacketSize = 1024
	onlineWindow  = 60 * time.Second
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type User struct {
	Name     string
	Addr     *net.UDPAddr
	LastSeen time.Time
}

type UDPMessage struct {
	Server     string     `json:"server"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	Type       string     `json:"type"`
	ID         string     `json:"id"`
	Name       string     `json:"name,omitempty"`
	ClientAddr string     `json:"client_addr,omitempty"`
	Text       string     `json:"text,omitempty"`
	RefID      string     `json:"ref_id,omitempty"`
	Status     string     `json:"status,omitempty"`
	Code       string     `json:"code,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Users      []UserInfo `json:"users,omitempty"`
}

type UserInfo struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type Server struct {
	mu        sync.Mutex
	users     map[string]*User
	pending   map[string]*net.UDPAddr
	udpConn   *net.UDPConn
	serverTag string
}

func NewServer(conn *net.UDPConn) *Server {
	return &Server{
		users:   make(map[string]*User),
		pending: make(map[string]*net.UDPAddr),
		udpConn: conn,
	}
}

func (s *Server) serve() {
	buf := make([]byte, maxPacketSize)
	for {
		n, addr, err := s.udpConn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("udp read error: %v", err)
			continue
		}
		if n <= 0 {
			continue
		}
		if n > maxPacketSize {
			s.sendErr(addr, "TOO_LONG", "message exceeds 1024 bytes", "")
			continue
		}

		var msg UDPMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			s.sendErr(addr, "BAD_FORMAT", "invalid json", "")
			continue
		}

		if msg.Type == "" {
			s.sendErr(addr, "UNKNOWN_TYPE", "missing type", msg.ID)
			continue
		}

		s.handleMessage(strings.ToUpper(msg.Type), msg, addr)
	}
}

func (s *Server) handleMessage(msgType string, msg UDPMessage, addr *net.UDPAddr) {
	switch msgType {
	case "JOIN":
		s.handleJoin(msg, addr)
	case "LIST":
		s.handleList(msg, addr)
	case "MSG":
		s.handleMsg(msg, addr)
	case "ACK":
		s.handleAck(msg, addr)
	case "ERR":
		log.Printf("client error from %s: %s", addr.String(), msg.Detail)
	default:
		s.sendErr(addr, "UNKNOWN_TYPE", "unknown message type", msg.ID)
	}
}

func (s *Server) handleJoin(msg UDPMessage, addr *net.UDPAddr) {
	name, err := normalizeName(msg.From)
	if err != nil {
		s.sendErr(addr, "USER_INVALID", err.Error(), msg.ID)
		return
	}
	if name == "" {
		name = normalizeOrEmpty(msg.Name)
	}
	if name == "" {
		s.sendErr(addr, "USER_INVALID", "missing user", msg.ID)
		return
	}

	s.mu.Lock()
	s.users[name] = &User{Name: name, Addr: addr, LastSeen: time.Now()}
	s.mu.Unlock()

	log.Printf("join: %s @ %s", name, addr.String())
}

func (s *Server) handleList(msg UDPMessage, addr *net.UDPAddr) {
	users := s.snapshotUsers()

	resp := UDPMessage{
		Server: s.serverTag,
		From:   "server",
		To:     msg.From,
		Type:   "USERS",
		ID:     newUUID(),
		Users:  users,
	}
	s.sendUDP(addr, resp)
}

func (s *Server) handleMsg(msg UDPMessage, addr *net.UDPAddr) {
	from, err := normalizeName(msg.From)
	if err != nil {
		s.sendErr(addr, "USER_INVALID", err.Error(), msg.ID)
		return
	}
	to, err := normalizeName(msg.To)
	if err != nil {
		s.sendErr(addr, "USER_INVALID", err.Error(), msg.ID)
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		s.sendErr(addr, "BAD_FORMAT", "missing text", msg.ID)
		return
	}

	s.touchUser(from, addr)

	recipient := s.getUser(to)
	if recipient == nil || !s.isOnline(recipient) {
		s.sendErr(addr, "USER_NOT_FOUND", "recipient not online", msg.ID)
		return
	}

	forward := UDPMessage{
		Server: s.serverTag,
		From:   from,
		To:     to,
		Type:   "MSG",
		ID:     msg.ID,
		Text:   msg.Text,
	}
	s.sendUDP(recipient.Addr, forward)

	s.mu.Lock()
	s.pending[msg.ID] = addr
	s.mu.Unlock()
}

func (s *Server) handleAck(msg UDPMessage, addr *net.UDPAddr) {
	ref := strings.TrimSpace(msg.RefID)
	if ref == "" {
		return
	}

	s.mu.Lock()
	senderAddr, ok := s.pending[ref]
	delete(s.pending, ref)
	s.mu.Unlock()

	if !ok || senderAddr == nil {
		return
	}

	forward := UDPMessage{
		Server: s.serverTag,
		From:   msg.From,
		To:     msg.To,
		Type:   "ACK",
		ID:     newUUID(),
		RefID:  ref,
		Status: "received",
	}
	s.sendUDP(senderAddr, forward)

	s.touchUser(normalizeOrEmpty(msg.From), addr)
}

func (s *Server) snapshotUsers() []UserInfo {
	now := time.Now()
	list := []UserInfo{}
	s.mu.Lock()
	for name, user := range s.users {
		online := now.Sub(user.LastSeen) <= onlineWindow
		list = append(list, UserInfo{Name: name, Online: online})
	}
	s.mu.Unlock()
	return list
}

func (s *Server) getUser(name string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[name]
}

func (s *Server) touchUser(name string, addr *net.UDPAddr) {
	if name == "" {
		return
	}
	s.mu.Lock()
	if user, ok := s.users[name]; ok {
		user.LastSeen = time.Now()
		user.Addr = addr
	} else {
		s.users[name] = &User{Name: name, Addr: addr, LastSeen: time.Now()}
	}
	s.mu.Unlock()
}

func (s *Server) isOnline(user *User) bool {
	if user == nil {
		return false
	}
	return time.Since(user.LastSeen) <= onlineWindow
}

func (s *Server) sendErr(addr *net.UDPAddr, code, detail, refID string) {
	msg := UDPMessage{
		Server: s.serverTag,
		From:   "server",
		To:     "",
		Type:   "ERR",
		ID:     newUUID(),
		Code:   code,
		Detail: detail,
		RefID:  refID,
	}
	s.sendUDP(addr, msg)
}

func (s *Server) sendUDP(addr *net.UDPAddr, msg UDPMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if len(data) > maxPacketSize {
		return
	}
	_, _ = s.udpConn.WriteToUDP(data, addr)
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

func normalizeOrEmpty(input string) string {
	name := strings.TrimSpace(input)
	if name == "" || !usernameRe.MatchString(name) || len(name) > 20 {
		return ""
	}
	return strings.ToLower(name)
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

func main() {
	port := flag.Int("port", 9999, "UDP port")
	serverTag := flag.String("tag", "mock-server", "server name to include in JSON")
	flag.Parse()

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: *port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer conn.Close()

	srv := NewServer(conn)
	srv.serverTag = *serverTag

	log.Printf("Mock UDP server listening on %s", addr.String())
	srv.serve()
}
