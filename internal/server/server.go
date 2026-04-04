package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dotaikit/browse/internal/browser"
	"github.com/dotaikit/browse/internal/state"
)

type Server struct {
	bm          *browser.Manager
	token       string
	port        int
	idleTimeout time.Duration
	idleTimer   *time.Timer
	mu          sync.Mutex
}

type commandRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func New(chromeURL string, port int, token string) (*Server, error) {
	bm, err := browser.New(chromeURL)
	if err != nil {
		return nil, fmt.Errorf("connect to Chrome: %w", err)
	}
	return NewWithManager(bm, port, token)
}

// NewWithManager creates a server from an already-initialized browser manager.
func NewWithManager(bm *browser.Manager, port int, token string) (*Server, error) {
	if bm == nil {
		return nil, fmt.Errorf("browser manager is required")
	}
	return &Server{
		bm:          bm,
		token:       token,
		port:        port,
		idleTimeout: 30 * time.Minute,
	}, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/command", s.authMiddleware(s.handleCommand))
	mux.HandleFunc("/refs", s.authMiddleware(s.handleRefs))

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.port = listener.Addr().(*net.TCPAddr).Port

	// Write state file
	st := &state.State{
		PID:       os.Getpid(),
		Port:      s.port,
		Token:     s.token,
		StartedAt: time.Now(),
		ChromeURL: s.bm.ChromeURL(),
	}
	if err := state.Write(st); err != nil {
		listener.Close()
		return fmt.Errorf("write state: %w", err)
	}

	// Start idle timer
	s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
		log.Println("idle timeout, shutting down")
		s.Shutdown()
		os.Exit(0)
	})

	log.Printf("browse server listening on 127.0.0.1:%d", s.port)
	return http.Serve(listener, mux)
}

func (s *Server) Shutdown() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.bm.Close()
	state.Remove()
}

func (s *Server) resetIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.idleTimeout)
	}
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || auth[7:] != s.token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "healthy",
		"uptime": time.Since(s.bm.StartedAt()).String(),
		"url":    s.bm.CurrentURL(),
	})
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s.resetIdle()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var req commandRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, "parse request: "+err.Error(), http.StatusBadRequest)
		return
	}

	result, err := s.bm.Execute(req.Command, req.Args)
	if err != nil {
		switch {
		case errors.Is(err, browser.ErrStopRequested):
			writeControlResponseAndExit(w, "Stopping browse server...", s)
			return
		case errors.Is(err, browser.ErrRestartRequested):
			writeControlResponseAndExit(w, "Restarting browse server...", s)
			return
		}
		writeError(w, friendlyErrorMessage(err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(result))
}

func writeControlResponseAndExit(w http.ResponseWriter, message string, srv *Server) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(message))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	go func() {
		// Let the response flush before terminating the process.
		time.Sleep(100 * time.Millisecond)
		srv.Shutdown()
		os.Exit(0)
	}()
}

func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request) {
	refs := s.bm.GetRefs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(refs)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func friendlyErrorMessage(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "ref ") && strings.Contains(lower, "not found"):
		return "Element not found. Run 'browse snapshot' to refresh refs."
	case strings.Contains(lower, "cannot find node with given id"):
		return "Element no longer exists on the page. Run 'browse snapshot' to refresh refs."
	case strings.Contains(lower, "no node found for given backend id"):
		return "Element no longer exists on the page. Run 'browse snapshot' to refresh refs."
	case strings.Contains(lower, "failed to find element matching selector"):
		return "Element not found for the given selector. Run 'browse snapshot -i' to inspect current elements."
	case strings.Contains(lower, "could not compute box model"):
		return "Element not found for the given selector. Run 'browse snapshot -i' to inspect current elements."
	case strings.Contains(lower, "could not find node for selector"):
		return "Element not found for the given selector. Run 'browse snapshot -i' to inspect current elements."
	case strings.Contains(lower, "could not resolve node"):
		return "Element not found for the given selector. Run 'browse snapshot -i' to inspect current elements."
	case strings.Contains(lower, "context deadline exceeded"):
		return "Operation timed out. Run 'browse wait 1000' and retry."
	case strings.Contains(lower, "invalid tab index"):
		return "Tab index is invalid. Run 'browse tabs' and choose a valid index."
	default:
		return raw
	}
}
