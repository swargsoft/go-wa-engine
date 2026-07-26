// cmd/server/main.go - platform-independent HTTP server for wa-engine
//
// BUILD FOR MAC (testing):
//   go build -o waengine ./cmd/server && ./waengine --port 8080 --data ./wa-data
//
// BUILD FOR LINUX:
//   GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o waengine-linux ./cmd/server
//
// SERVICE INSTALL (run once after downloading):
//   sudo ./waengine --install-service   # registers + starts background service
//   sudo ./waengine --uninstall-service # stops + removes service
//
// API:
//   POST   /api/sessions/:name/pair           Start QR pairing
//   POST   /api/sessions/:name/pair-code      Start phone pairing (code-based)
//   POST   /api/sessions/:name/start          Connect existing session
//   POST   /api/sessions/:name/stop           Disconnect
//   DELETE /api/sessions/:name                Remove permanently
//   GET    /api/sessions/:name/status         Status JSON
//   GET    /api/sessions/:name/qr             Current QR string
//   GET    /api/sessions/:name/pairing-code   Current pairing code string
//   GET    /api/sessions/:name/events         Poll next event (non-blocking)
//   GET    /api/sessions/:name/stream         SSE stream of events
//   POST   /api/sessions/:name/send/text      Send text message
//   POST   /api/sessions/:name/send/image     Send image with caption
//   POST   /api/sessions/:name/active         Mark session active (anti-ban)
//   GET    /api/sessions                      List all sessions
//   GET    /api/health                        Health check
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	core "github.com/mml/wa-engine/core"
)

var (
	flagPort      = flag.Int("port", 8080, "HTTP server port")
	flagData      = flag.String("data", "", "Data directory for session storage (default: ~/.wa-engine)")
	flagHost      = flag.String("host", "127.0.0.1", "Bind address (127.0.0.1 = local only)")
	flagAPIKey    = flag.String("key", "", "Optional API key (empty = no auth)")
	flagVer       = flag.Bool("version", false, "Print version and exit")
	flagInstall   = flag.Bool("install-service", false, "Install and start wa-engine as a system service")
	flagUninstall = flag.Bool("uninstall-service", false, "Stop and remove the wa-engine system service")
)

type server struct {
	sm *core.SessionManager
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./wa-data"
	}
	return filepath.Join(home, ".wa-engine")
}

func main() {
	flag.Parse()

	if *flagVer {
		fmt.Printf("wa-engine %s\n", core.Version)
		os.Exit(0)
	}

	if *flagInstall {
		if err := installService(); err != nil {
			log.Fatalf("install-service failed: %v", err)
		}
		os.Exit(0)
	}

	if *flagUninstall {
		if err := uninstallService(); err != nil {
			log.Fatalf("uninstall-service failed: %v", err)
		}
		os.Exit(0)
	}

	if isWindowsServiceRun() {
		return
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	runServer(stop)
}

func runServer(stop <-chan os.Signal) {
	dataPath := *flagData
	if dataPath == "" {
		dataPath = defaultDataDir()
	}
	dataDir, err := filepath.Abs(dataPath)
	if err != nil {
		log.Fatalf("Invalid data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		log.Fatalf("Cannot create data dir: %v", err)
	}

	sm, err := core.NewSessionManager(dataDir)
	if err != nil {
		log.Fatalf("Failed to init session manager: %v", err)
	}

	sleepMon := core.NewSleepMonitor(sm)
	go sleepMon.Start()

	srv := &server{sm: sm}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/sessions", srv.handleSessions)
	mux.HandleFunc("/api/sessions/", srv.routeSession)

	addr := fmt.Sprintf("%s:%d", *flagHost, *flagPort)
	httpSrv := &http.Server{
		Addr:        addr,
		Handler:     authMiddleware(corsMiddleware(mux)),
		ReadTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("wa-engine %s listening on http://%s", core.Version, addr)
		log.Printf("Data directory: %s", dataDir)
		if *flagAPIKey != "" {
			log.Printf("API key auth enabled")
		}
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")
	sleepMon.Stop()
	sm.StopAll()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Println("Stopped.")
}

// --- Middleware ---

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *flagAPIKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		if key != *flagAPIKey {
			jsonError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid or missing API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Route dispatcher ---

func (s *server) routeSession(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/sessions/:name[/:action[/:sub]]
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		s.handleSessions(w, r)
		return
	}

	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		s.handleRemoveSession(w, r, name)
	case action == "" && r.Method == http.MethodGet:
		s.handleSessionStatus(w, r, name)
	case action == "pair" && r.Method == http.MethodPost:
		s.handlePair(w, r, name)
	case action == "pair-code" && r.Method == http.MethodPost:
		s.handlePhonePair(w, r, name)
	case action == "start" && r.Method == http.MethodPost:
		s.handleStart(w, r, name)
	case action == "stop" && r.Method == http.MethodPost:
		s.handleStop(w, r, name)
	case action == "status" && r.Method == http.MethodGet:
		s.handleSessionStatus(w, r, name)
	case action == "qr" && r.Method == http.MethodGet:
		s.handleGetQR(w, r, name)
	case action == "pairing-code" && r.Method == http.MethodGet:
		s.handleGetPairingCode(w, r, name)
	case action == "events" && r.Method == http.MethodGet:
		s.handlePollEvent(w, r, name)
	case action == "stream" && r.Method == http.MethodGet:
		s.handleSSEStream(w, r, name)
	case action == "active" && r.Method == http.MethodPost:
		s.handleMarkActive(w, r, name)
	case action == "send" && sub == "text" && r.Method == http.MethodPost:
		s.handleSendText(w, r, name)
	case action == "send" && sub == "image" && r.Method == http.MethodPost:
		s.handleSendImage(w, r, name)
	case action == "profile" && sub == "" && r.Method == http.MethodGet:
		s.handleGetOwnProfile(w, r, name)
	case action == "profile" && sub != "" && r.Method == http.MethodGet:
		s.handleGetUserProfile(w, r, name, sub)
	default:
		jsonError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("Unknown route: %s %s", r.Method, r.URL.Path))
	}
}

// --- Handlers ---

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]interface{}{
		"status":   "ok",
		"version":  core.Version,
		"sessions": s.sm.GetSessionCount(),
		"time":     time.Now().UnixMilli(),
	})
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET")
		return
	}
	// GetAllSessionsInfo() returns a JSON string directly
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"sessions":%s}`, s.sm.GetAllSessionsInfo())
}

func (s *server) handlePair(w http.ResponseWriter, r *http.Request, name string) {
	// sm.StartPairing(sessionName string) error
	if err := s.sm.StartPairing(name); err != nil {
		jsonError(w, http.StatusBadRequest, "pair_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{
		"status":  "pairing_started",
		"session": name,
		"message": "Poll /events or /qr for QR code",
	})
}

func (s *server) handleStart(w http.ResponseWriter, r *http.Request, name string) {
	// sm.Start(sessionName string) error
	if err := s.sm.Start(name); err != nil {
		jsonError(w, http.StatusBadRequest, "start_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "starting", "session": name})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request, name string) {
	// sm.Stop(sessionName string) error
	if err := s.sm.Stop(name); err != nil {
		jsonError(w, http.StatusBadRequest, "stop_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "stopped", "session": name})
}

func (s *server) handleRemoveSession(w http.ResponseWriter, r *http.Request, name string) {
	if err := s.sm.LogoutSession(name); err != nil {
		jsonError(w, http.StatusBadRequest, "remove_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "removed", "session": name})
}

func (s *server) handleSessionStatus(w http.ResponseWriter, r *http.Request, name string) {
	// sm.GetSessionInfoJSON(sessionName string) string - returns "" if not found
	info := s.sm.GetSessionInfoJSON(name)
	if info == "" {
		jsonError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("Session %q not found", name))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(info))
}

func (s *server) handleGetQR(w http.ResponseWriter, r *http.Request, name string) {
	// sm.GetQR(sessionName string) string
	qr := s.sm.GetQR(name)
	jsonOK(w, map[string]string{"session": name, "qr": qr})
}

func (s *server) handleGetPairingCode(w http.ResponseWriter, r *http.Request, name string) {
	code := s.sm.GetPairingCode(name)
	jsonOK(w, map[string]string{"session": name, "pairing_code": code})
}

type phonePairRequest struct {
	Phone string `json:"phone"`
}

func (s *server) handlePhonePair(w http.ResponseWriter, r *http.Request, name string) {
	var req phonePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.Phone == "" {
		jsonError(w, http.StatusBadRequest, "missing_fields", "'phone' is required")
		return
	}
	code, err := s.sm.StartPhonePairing(name, req.Phone)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "pair_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{
		"status":       "pairing_started",
		"session":      name,
		"pairing_code": code,
		"message":      "Enter this code in WhatsApp -> Linked Devices -> Link a Device",
	})
}

func (s *server) handlePollEvent(w http.ResponseWriter, r *http.Request, name string) {
	// sm.PollEvent(sessionName string) string
	event := s.sm.PollEvent(name)
	w.Header().Set("Content-Type", "application/json")
	if event == "" {
		w.Write([]byte(`{"event":null}`))
		return
	}
	fmt.Fprintf(w, `{"event":%s}`, event)
}

func (s *server) handleSSEStream(w http.ResponseWriter, r *http.Request, name string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "no_sse", "SSE not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-ticker.C:
			// sm.PollEvent(sessionName string) string
			if event := s.sm.PollEvent(name); event != "" {
				fmt.Fprintf(w, "data: %s\n\n", event)
				flusher.Flush()
			}
		}
	}
}

func (s *server) handleMarkActive(w http.ResponseWriter, r *http.Request, name string) {
	// Defined in session_extra.go: MarkActiveSession(sessionName string)
	s.sm.MarkActiveSession(name)
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- Send handlers ---

type sendTextRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

func (s *server) handleSendText(w http.ResponseWriter, r *http.Request, name string) {
	var req sendTextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.To == "" || req.Text == "" {
		jsonError(w, http.StatusBadRequest, "missing_fields", "'to' and 'text' are required")
		return
	}
	// sm.SendText(to, sessionName, text string) (string, error)
	msgID, err := s.sm.SendText(req.To, name, req.Text)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "send_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{"id": msgID, "status": "sent"})
}

type sendImageRequest struct {
	To      string `json:"to"`
	Source  string `json:"source"`  // URL or base64 data URI
	Caption string `json:"caption"` // optional
}

func (s *server) handleSendImage(w http.ResponseWriter, r *http.Request, name string) {
	var req sendImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if req.To == "" || req.Source == "" {
		jsonError(w, http.StatusBadRequest, "missing_fields", "'to' and 'source' are required")
		return
	}
	// sm.SendImageWithCaption(to, sessionName, imageSource, caption string) (string, error)
	msgID, err := s.sm.SendImageWithCaption(req.To, name, req.Source, req.Caption)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "send_error", err.Error())
		return
	}
	jsonOK(w, map[string]string{"id": msgID, "status": "sent"})
}

func (s *server) handleGetOwnProfile(w http.ResponseWriter, r *http.Request, name string) {
	profileJSON, err := s.sm.GetOwnProfile(name)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "profile_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(profileJSON))
}

func (s *server) handleGetUserProfile(w http.ResponseWriter, r *http.Request, name, jid string) {
	profileJSON, err := s.sm.GetUserProfile(name, jid)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "profile_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(profileJSON))
}

// --- Response helpers ---

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error":   errCode,
		"message": msg,
	})
}
