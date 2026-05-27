package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultPort   = 8787
	serviceName   = "Life DB"
	serviceType   = "_life-db._tcp"
	serviceDomain = "local."
)

type Entry struct {
	ID             string `json:"id"`
	Content        string `json:"content"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
	DeletedAt      *int64 `json:"deleted_at,omitempty"`
	Version        int64  `json:"version"`
	SourceDeviceID string `json:"source_device_id"`
}

type APIError struct {
	Error string `json:"error"`
}

type Server struct {
	db       *sql.DB
	dataDir  string
	deviceID string
	hub      *Hub
}

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

func newHub() *Hub {
	return &Hub{clients: map[*websocket.Conn]bool{}}
}

func (h *Hub) add(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = true
}

func (h *Hub) remove(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
	_ = conn.Close()
}

func (h *Hub) broadcast(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for conn := range h.clients {
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			_ = conn.Close()
			delete(h.clients, conn)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	switch os.Args[1] {
	case "serve":
		serveCmd(os.Args[2:])
	case "add":
		addCmd(os.Args[2:])
	case "list":
		listCmd(os.Args[2:])
	case "tui":
		tuiCmd(os.Args[2:])
	case "export-md":
		exportMarkdownCmd(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Println(`life-db

Commands:
  serve       start HTTP/WebSocket server + mDNS discovery
  add TEXT    add one entry through local server
  list        print recent entries from local server
  tui         open terminal UI (Bubble Tea)
  export-md   export entries by day as Markdown

Examples:
  go run . serve
  go run . add "关屏，去晒衣服"
  go run . tui`)
}

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "listen port")
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	_ = fs.Parse(args)

	server, err := newServer(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer server.db.Close()

	zeroconfServer, err := publishMDNS(*port, server.deviceID)
	if err != nil {
		log.Printf("mDNS publish failed: %v", err)
	} else {
		defer zeroconfServer.Shutdown()
	}

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Life DB serving on http://127.0.0.1:%d", *port)
	log.Printf("Data dir: %s", *dataDir)
	log.Printf("Device ID: %s", server.deviceID)
	log.Printf("mDNS service: %s.%s%s", serviceName, serviceType, serviceDomain)

	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func addCmd(args []string) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		fmt.Fprintln(os.Stderr, "usage: life-db add <text>")
		os.Exit(1)
	}

	entry := map[string]any{
		"content":          text,
		"source_device_id": hostnameDeviceID(),
	}
	body, _ := json.Marshal(entry)

	resp, err := http.Post("http://127.0.0.1:8787/api/entries", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "server not reachable: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "add failed: %s\n", strings.TrimSpace(string(b)))
		os.Exit(1)
	}
	fmt.Println("added")
}

func listCmd(args []string) {
	resp, err := http.Get("http://127.0.0.1:8787/api/entries")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server not reachable: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "list failed: %s\n", strings.TrimSpace(string(b)))
		os.Exit(1)
	}
	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		if e.DeletedAt != nil {
			continue
		}
		fmt.Printf("%s  %s\n", formatClock(e.CreatedAt), e.Content)
	}
}

func exportMarkdownCmd(args []string) {
	fs := flag.NewFlagSet("export-md", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "data directory")
	outDir := fs.String("out", filepath.Join(defaultDataDir(), "export"), "output directory")
	_ = fs.Parse(args)

	server, err := newServer(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer server.db.Close()

	entries, err := server.listEntries(false)
	if err != nil {
		log.Fatal(err)
	}
	byDate := map[string][]Entry{}
	for _, e := range entries {
		day := time.UnixMilli(e.CreatedAt).Format("2006-01-02")
		byDate[day] = append(byDate[day], e)
	}
	for day, dayEntries := range byDate {
		sort.Slice(dayEntries, func(i, j int) bool { return dayEntries[i].CreatedAt < dayEntries[j].CreatedAt })
		year := day[:4]
		month := day[5:7]
		dir := filepath.Join(*outDir, year, month)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(dir, day+".md")
		var b strings.Builder
		for _, e := range dayEntries {
			lines := strings.Split(e.Content, "\n")
			title := strings.TrimSpace(lines[0])
			if title == "" {
				title = "未命名"
			}
			b.WriteString("# ")
			b.WriteString(formatClock(e.CreatedAt))
			b.WriteString(" ")
			b.WriteString(title)
			b.WriteString("\n")
			if len(lines) > 1 {
				body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
				if body != "" {
					b.WriteString(body)
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println(path)
	}
}

func defaultDataDir() string {
	if v := os.Getenv("LIFE_DB_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".life-db"
	}
	return filepath.Join(home, ".local", "share", "life-db")
}

func hostnameDeviceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "computer"
	}
	return host
}

func newServer(dataDir string) (*Server, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	deviceID, err := loadOrCreateDeviceID(dataDir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "life.db")
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	if err := initDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Server{db: db, dataDir: dataDir, deviceID: deviceID, hub: newHub()}, nil
}

func loadOrCreateDeviceID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "device_id")
	b, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if id != "" {
			return id, nil
		}
	}
	id := hostnameDeviceID() + "-" + uuid.NewString()[:8]
	return id, os.WriteFile(path, []byte(id+"\n"), 0o600)
}

func initDB(db *sql.DB) error {
	_, err := db.Exec(`
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS entries (
  id TEXT PRIMARY KEY,
  content TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER,
  version INTEGER NOT NULL,
  source_device_id TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at);
CREATE INDEX IF NOT EXISTS idx_entries_updated_at ON entries(updated_at);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  last_seen_at INTEGER
);
`)
	return err
}

func publishMDNS(port int, deviceID string) (*zeroconf.Server, error) {
	text := []string{"app=life-db", "version=1", "device_id=" + deviceID}
	return zeroconf.Register(serviceName+" - "+hostnameDeviceID(), serviceType, serviceDomain, port, text, nil)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/device", s.handleDevice)
	mux.HandleFunc("/api/entries", s.handleEntries)
	mux.HandleFunc("/api/entries/", s.handleEntryByID)
	mux.HandleFunc("/api/sync", s.handleSync)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/", s.handleStatic)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": "life-db", "device_id": s.deviceID})
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":              "life-db",
		"device_id":        s.deviceID,
		"device_name":      hostnameDeviceID(),
		"protocol_version": 1,
	})
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		includeDeleted := r.URL.Query().Get("include_deleted") == "true"
		entries, err := s.listEntries(includeDeleted)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, entries)
	case http.MethodPost:
		var req struct {
			ID             string `json:"id"`
			Content        string `json:"content"`
			CreatedAt      int64  `json:"created_at"`
			SourceDeviceID string `json:"source_device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		req.Content = strings.TrimSpace(req.Content)
		if req.Content == "" {
			writeError(w, http.StatusBadRequest, errors.New("content is required"))
			return
		}
		entry, err := s.createEntry(req.ID, req.Content, req.CreatedAt, req.SourceDeviceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.broadcastChange([]Entry{entry})
		writeJSON(w, http.StatusCreated, entry)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEntryByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/entries/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Content string `json:"content"`
			Version int64  `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		entry, conflict, err := s.updateEntry(id, req.Content, req.Version)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if conflict {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "version conflict", "current": entry})
			return
		}
		s.broadcastChange([]Entry{entry})
		writeJSON(w, http.StatusOK, entry)
	case http.MethodDelete:
		version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		entry, conflict, err := s.deleteEntry(id, version)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if conflict {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "version conflict", "current": entry})
			return
		}
		s.broadcastChange([]Entry{entry})
		writeJSON(w, http.StatusOK, entry)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type SyncChange struct {
	Action      string `json:"action"`
	BaseVersion int64  `json:"base_version"`
	Entry       Entry  `json:"entry"`
}

type SyncRequest struct {
	DeviceID string       `json:"device_id"`
	Changes  []SyncChange `json:"changes"`
}

type SyncResponse struct {
	Entries   []Entry  `json:"entries"`
	Conflicts []string `json:"conflicts"`
	ServerAt  int64    `json:"server_at"`
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	changed, conflicts, err := s.applySyncChanges(req.Changes, req.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	entries, err := s.listEntries(true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if len(changed) > 0 {
		s.broadcastChange(changed)
	}
	writeJSON(w, http.StatusOK, SyncResponse{Entries: entries, Conflicts: conflicts, ServerAt: nowMs()})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.add(conn)
	defer s.hub.remove(conn)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Life DB</title><body style="font-family:sans-serif;padding:2rem"><h1>Life DB server is running</h1><p>API is available. Run the web client with <code>cd web && npm install && npm run dev</code>.</p></body>`)
}

func (s *Server) broadcastChange(entries []Entry) {
	s.hub.broadcast(map[string]any{
		"type":    "entries_changed",
		"entries": entries,
		"at":      nowMs(),
	})
}

func (s *Server) listEntries(includeDeleted bool) ([]Entry, error) {
	query := `SELECT id, content, created_at, updated_at, deleted_at, version, source_device_id FROM entries`
	if !includeDeleted {
		query += ` WHERE deleted_at IS NULL`
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(scanner rowScanner) (Entry, error) {
	var entry Entry
	var deleted sql.NullInt64
	if err := scanner.Scan(&entry.ID, &entry.Content, &entry.CreatedAt, &entry.UpdatedAt, &deleted, &entry.Version, &entry.SourceDeviceID); err != nil {
		return entry, err
	}
	if deleted.Valid {
		v := deleted.Int64
		entry.DeletedAt = &v
	}
	return entry, nil
}

func (s *Server) getEntry(id string) (Entry, bool, error) {
	row := s.db.QueryRow(`SELECT id, content, created_at, updated_at, deleted_at, version, source_device_id FROM entries WHERE id = ?`, id)
	entry, err := scanEntry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (s *Server) createEntry(id, content string, createdAt int64, sourceDeviceID string) (Entry, error) {
	if id == "" {
		id = uuid.NewString()
	}
	if createdAt == 0 {
		createdAt = nowMs()
	}
	updatedAt := nowMs()
	if sourceDeviceID == "" {
		sourceDeviceID = s.deviceID
	}
	entry := Entry{ID: id, Content: strings.TrimSpace(content), CreatedAt: createdAt, UpdatedAt: updatedAt, Version: 1, SourceDeviceID: sourceDeviceID}
	_, err := s.db.Exec(`INSERT INTO entries (id, content, created_at, updated_at, deleted_at, version, source_device_id) VALUES (?, ?, ?, ?, NULL, ?, ?)`, entry.ID, entry.Content, entry.CreatedAt, entry.UpdatedAt, entry.Version, entry.SourceDeviceID)
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Server) updateEntry(id, content string, version int64) (Entry, bool, error) {
	current, ok, err := s.getEntry(id)
	if err != nil || !ok {
		return current, false, err
	}
	if version > 0 && current.Version != version {
		return current, true, nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		content = current.Content
	}
	updatedAt := nowMs()
	_, err = s.db.Exec(`UPDATE entries SET content = ?, updated_at = ?, version = version + 1 WHERE id = ?`, content, updatedAt, id)
	if err != nil {
		return Entry{}, false, err
	}
	updated, _, err := s.getEntry(id)
	return updated, false, err
}

func (s *Server) deleteEntry(id string, version int64) (Entry, bool, error) {
	current, ok, err := s.getEntry(id)
	if err != nil || !ok {
		return current, false, err
	}
	if version > 0 && current.Version != version {
		return current, true, nil
	}
	deletedAt := nowMs()
	_, err = s.db.Exec(`UPDATE entries SET deleted_at = ?, updated_at = ?, version = version + 1 WHERE id = ?`, deletedAt, deletedAt, id)
	if err != nil {
		return Entry{}, false, err
	}
	deleted, _, err := s.getEntry(id)
	return deleted, false, err
}

func (s *Server) mustGetEntry(id string) (Entry, bool, error) {
	return s.getEntry(id)
}

func (s *Server) applySyncChanges(changes []SyncChange, sourceDeviceID string) ([]Entry, []string, error) {
	var changed []Entry
	var conflicts []string

	for _, change := range changes {
		entry := change.Entry
		if entry.ID == "" {
			entry.ID = uuid.NewString()
		}
		if entry.SourceDeviceID == "" {
			entry.SourceDeviceID = sourceDeviceID
		}
		if entry.SourceDeviceID == "" {
			entry.SourceDeviceID = "unknown"
		}
		entry.Content = strings.TrimSpace(entry.Content)
		if entry.CreatedAt == 0 {
			entry.CreatedAt = nowMs()
		}
		if entry.UpdatedAt == 0 {
			entry.UpdatedAt = nowMs()
		}

		current, exists, err := s.getEntry(entry.ID)
		if err != nil {
			return changed, conflicts, err
		}

		switch change.Action {
		case "create":
			if exists {
				continue
			}
			if entry.Content == "" {
				continue
			}
			entry.Version = 1
			_, err := s.db.Exec(`INSERT INTO entries (id, content, created_at, updated_at, deleted_at, version, source_device_id) VALUES (?, ?, ?, ?, NULL, ?, ?)`, entry.ID, entry.Content, entry.CreatedAt, nowMs(), entry.Version, entry.SourceDeviceID)
			if err != nil {
				return changed, conflicts, err
			}
			stored, _, _ := s.getEntry(entry.ID)
			changed = append(changed, stored)
		case "update":
			if !exists {
				conflicts = append(conflicts, entry.ID)
				continue
			}
			if current.Version != change.BaseVersion {
				conflicts = append(conflicts, entry.ID)
				continue
			}
			if entry.Content == "" {
				continue
			}
			updatedAt := nowMs()
			_, err := s.db.Exec(`UPDATE entries SET content = ?, updated_at = ?, version = version + 1 WHERE id = ?`, entry.Content, updatedAt, entry.ID)
			if err != nil {
				return changed, conflicts, err
			}
			stored, _, _ := s.getEntry(entry.ID)
			changed = append(changed, stored)
		case "delete":
			if !exists {
				continue
			}
			if current.Version != change.BaseVersion {
				conflicts = append(conflicts, entry.ID)
				continue
			}
			deletedAt := nowMs()
			_, err := s.db.Exec(`UPDATE entries SET deleted_at = ?, updated_at = ?, version = version + 1 WHERE id = ?`, deletedAt, deletedAt, entry.ID)
			if err != nil {
				return changed, conflicts, err
			}
			stored, _, _ := s.getEntry(entry.ID)
			changed = append(changed, stored)
		default:
			// Last-write-wins fallback for future tools.
			if !exists {
				if entry.Version <= 0 {
					entry.Version = 1
				}
				_, err := s.db.Exec(`INSERT INTO entries (id, content, created_at, updated_at, deleted_at, version, source_device_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, entry.ID, entry.Content, entry.CreatedAt, entry.UpdatedAt, entry.DeletedAt, entry.Version, entry.SourceDeviceID)
				if err != nil {
					return changed, conflicts, err
				}
				stored, _, _ := s.getEntry(entry.ID)
				changed = append(changed, stored)
			} else if entry.UpdatedAt > current.UpdatedAt {
				_, err := s.db.Exec(`UPDATE entries SET content = ?, updated_at = ?, deleted_at = ?, version = ?, source_device_id = ? WHERE id = ?`, entry.Content, entry.UpdatedAt, entry.DeletedAt, entry.Version, entry.SourceDeviceID, entry.ID)
				if err != nil {
					return changed, conflicts, err
				}
				stored, _, _ := s.getEntry(entry.ID)
				changed = append(changed, stored)
			}
		}
	}

	return changed, conflicts, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, APIError{Error: err.Error()})
}

func nowMs() int64 { return time.Now().UnixMilli() }

func formatClock(ms int64) string { return time.UnixMilli(ms).Format("15:04") }

// ---------------- TUI ----------------

type tuiModel struct {
	entries []Entry
	input   string
	status  string
	cursor  int
}

type entriesMsg []Entry
type statusMsg string

func tuiCmd(args []string) {
	p := tea.NewProgram(tuiModel{status: "r 刷新 · enter 添加 · ↑/↓ 选择 · d 删除 · q 退出"}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

func (m tuiModel) Init() tea.Cmd { return fetchEntriesCmd }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.status = "刷新中"
			return m, fetchEntriesCmd
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case "enter":
			text := strings.TrimSpace(m.input)
			if text != "" {
				m.input = ""
				m.status = "添加中"
				return m, addEntryCmd(text)
			}
		case "d":
			if len(m.entries) > 0 && m.cursor >= 0 && m.cursor < len(m.entries) {
				entry := m.entries[m.cursor]
				m.status = "删除中"
				return m, deleteEntryCmd(entry)
			}
		default:
			if len(msg.String()) == 1 {
				m.input += msg.String()
			}
		}
	case entriesMsg:
		m.entries = []Entry(msg)
		if m.cursor >= len(m.entries) {
			m.cursor = len(m.entries) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.status = "已同步 " + time.Now().Format("15:04:05")
	case statusMsg:
		m.status = string(msg)
		return m, fetchEntriesCmd
	}
	return m, nil
}

func (m tuiModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("Life DB")
	var b strings.Builder
	b.WriteString(title + "\n\n")
	start := 0
	if len(m.entries) > 20 {
		start = len(m.entries) - 20
	}
	for i, e := range m.entries[start:] {
		idx := start + i
		prefix := "  "
		if idx == m.cursor {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%s  %s", prefix, formatClock(e.CreatedAt), oneLine(e.Content))
		if idx == m.cursor {
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(m.status) + "\n")
	b.WriteString("输入: " + m.input + "\n")
	return b.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " / ")
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80]) + "…"
	}
	return s
}

func fetchEntriesCmd() tea.Msg {
	resp, err := http.Get("http://127.0.0.1:8787/api/entries")
	if err != nil {
		return statusMsg("服务未启动：life-db serve")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusMsg("读取失败")
	}
	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return statusMsg("解析失败")
	}
	return entriesMsg(entries)
}

func addEntryCmd(text string) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]any{"content": text, "source_device_id": hostnameDeviceID()})
		resp, err := http.Post("http://127.0.0.1:8787/api/entries", "application/json", bytes.NewReader(body))
		if err != nil {
			return statusMsg("添加失败：服务未启动")
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return statusMsg("添加失败")
		}
		return statusMsg("已添加")
	}
}

func deleteEntryCmd(entry Entry) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://127.0.0.1:8787/api/entries/%s?version=%d", entry.ID, entry.Version), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return statusMsg("删除失败")
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return statusMsg("删除失败")
		}
		return statusMsg("已删除")
	}
}

// Keep stdin scanner linked when running under some terminals.
var _ = bufio.ErrInvalidUnreadByte
var _ = context.Background
var _ = net.IPv4
