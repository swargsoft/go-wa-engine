// waengine_server.go - gomobile entry point for mobile platforms.
//
// This file exposes a minimal API for embedding the wa-engine HTTP server
// inside a mobile app (Android foreground service, iOS background task).
//
// The PWA running in any browser on the device connects to localhost:PORT
// and uses the full REST API defined in cmd/server/main.go.
//
// ANDROID USAGE (from WaEngineService.kt):
//
//	Waengine.startHTTPServer(8080, dataDir)   // call from Service.onStartCommand
//	Waengine.stopHTTPServer()                 // call from Service.onDestroy
//
// GOMOBILE COMPATIBILITY:
// Only primitive types (string, int, bool) are used - no structs or interfaces.
package waengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	core "github.com/mml/wa-engine/core"
)

// httpServerState holds the running server instance.
// Only one HTTP server per process - mobile apps are single-session hosts.
var httpServerState struct {
	sync.Mutex
	srv *http.Server
	sm  *core.SessionManager
}

// StartHTTPServer starts the wa-engine REST API server on the given port.
// dataDir is the path to a writable directory for SQLite session storage.
//
// On Android pass context.getFilesDir().getAbsolutePath() + "/wa-engine".
// This is BLOCKING - call it from a goroutine in your Android Service.
//
// Returns an error string, empty string on success.
func StartHTTPServer(port int, dataDir string) string {
	httpServerState.Lock()
	if httpServerState.srv != nil {
		httpServerState.Unlock()
		return "" // already running - idempotent
	}

	sm, err := core.NewSessionManager(dataDir)
	if err != nil {
		httpServerState.Unlock()
		return err.Error()
	}

	mux := buildMux(sm)
	srv := &http.Server{
		Addr:        fmt.Sprintf("127.0.0.1:%d", port),
		Handler:     corsMiddleware(mux),
		ReadTimeout: 30 * time.Second,
	}

	httpServerState.srv = srv
	httpServerState.sm = sm
	httpServerState.Unlock()

	// ListenAndServe blocks until StopHTTPServer is called
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err.Error()
	}
	return ""
}

// StopHTTPServer gracefully shuts down the HTTP server.
// Safe to call even if the server is not running.
func StopHTTPServer() {
	httpServerState.Lock()
	defer httpServerState.Unlock()

	if httpServerState.sm != nil {
		httpServerState.sm.StopAll()
		httpServerState.sm = nil
	}
	if httpServerState.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServerState.srv.Shutdown(ctx)
		httpServerState.srv = nil
	}
}

// IsServerRunning returns true if the HTTP server is currently active.
func IsServerRunning() bool {
	httpServerState.Lock()
	defer httpServerState.Unlock()
	return httpServerState.srv != nil
}

// --- HTTP routing (mirrors cmd/server/main.go exactly) ---

func buildMux(sm *core.SessionManager) *http.ServeMux {
	s := &mobileServer{sm: sm}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/", s.routeSession)
	return mux
}

type mobileServer struct {
	sm *core.SessionManager
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *mobileServer) routeSession(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		s.handleSessions(w, r)
		return
	}
	name, action, sub := parts[0], "", ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if len(parts) > 2 {
		sub = parts[2]
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		jsonResp(w, 200, mapOf("status", runErr(s.sm.RemoveSession(name))))
	case action == "" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(s.sm.GetSessionInfoJSON(name)))
	case action == "pair" && r.Method == http.MethodPost:
		jsonResp(w, 200, mapOf("status", runErr(s.sm.StartPairing(name))))
	case action == "start" && r.Method == http.MethodPost:
		jsonResp(w, 200, mapOf("status", runErr(s.sm.Start(name))))
	case action == "stop" && r.Method == http.MethodPost:
		jsonResp(w, 200, mapOf("status", runErr(s.sm.Stop(name))))
	case action == "status" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(s.sm.GetSessionInfoJSON(name)))
	case action == "qr" && r.Method == http.MethodGet:
		jsonResp(w, 200, map[string]string{"session": name, "qr": s.sm.GetQR(name)})
	case action == "events" && r.Method == http.MethodGet:
		event := s.sm.PollEvent(name)
		w.Header().Set("Content-Type", "application/json")
		if event == "" {
			w.Write([]byte(`{"event":null}`))
		} else {
			fmt.Fprintf(w, `{"event":%s}`, event)
		}
	case action == "stream" && r.Method == http.MethodGet:
		s.handleSSE(w, r, name)
	case action == "active" && r.Method == http.MethodPost:
		s.sm.MarkActiveSession(name)
		jsonResp(w, 200, mapOf("status", "ok"))
	case action == "send" && sub == "text" && r.Method == http.MethodPost:
		var req struct {
			To   string `json:"to"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" || req.Text == "" {
			jsonResp(w, 400, mapOf("error", "to and text required"))
			return
		}
		id, err := s.sm.SendText(req.To, name, req.Text)
		if err != nil {
			jsonResp(w, 400, mapOf("error", err.Error()))
			return
		}
		jsonResp(w, 200, map[string]string{"id": id, "status": "sent"})
	case action == "send" && sub == "image" && r.Method == http.MethodPost:
		var req struct {
			To      string `json:"to"`
			Source  string `json:"source"`
			Caption string `json:"caption"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" || req.Source == "" {
			jsonResp(w, 400, mapOf("error", "to and source required"))
			return
		}
		id, err := s.sm.SendImageWithCaption(req.To, name, req.Source, req.Caption)
		if err != nil {
			jsonResp(w, 400, mapOf("error", err.Error()))
			return
		}
		jsonResp(w, 200, map[string]string{"id": id, "status": "sent"})
	default:
		jsonResp(w, 404, mapOf("error", "not found"))
	}
}

func (s *mobileServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"status":   "ok",
		"sessions": s.sm.GetSessionCount(),
		"time":     time.Now().UnixMilli(),
	})
}

func (s *mobileServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResp(w, 405, mapOf("error", "use GET"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"sessions":%s}`, s.sm.GetAllSessionsInfo())
}

func (s *mobileServer) handleSSE(w http.ResponseWriter, r *http.Request, name string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonResp(w, 500, mapOf("error", "sse not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	tick := time.NewTicker(50 * time.Millisecond)
	ping := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case <-tick.C:
			if ev := s.sm.PollEvent(name); ev != "" {
				fmt.Fprintf(w, "data: %s\n\n", ev)
				flusher.Flush()
			}
		}
	}
}

// --- helpers ---

func jsonResp(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func mapOf(k, v string) map[string]string { return map[string]string{k: v} }

func runErr(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ok"
}
