package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
)

// Supervisor owns listener generations so the entry endpoint can move without
// restarting the process. A new socket is bound before the old one is closed.
type Supervisor struct {
	mu      sync.Mutex
	handler http.Handler
	logger  *slog.Logger
	gen     int
	current *generation
}

type generation struct {
	server *http.Server
	ln     net.Listener
	addr   string
	gen    int
}

// NewSupervisor creates a supervisor for the given handler.
func NewSupervisor(handler http.Handler, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{handler: handler, logger: logger}
}

// Start binds addr and starts serving.
func (s *Supervisor) Start(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return fmt.Errorf("supervisor already started")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	g := s.startGenerationLocked(ln)
	s.current = g
	return nil
}

// Rebind binds addr before touching the current listener. On failure the old
// listener keeps serving.
func (s *Supervisor) Rebind(addr string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && s.current.addr == addr {
		return s.current.gen, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	g := s.startGenerationLocked(ln)
	old := s.current
	s.current = g
	if old != nil {
		_ = old.ln.Close()
		go func() {
			// No deadline: active streams are allowed to finish.
			_ = old.server.Shutdown(context.Background())
			s.logger.Info("listener generation retired", "generation", old.gen, "listen", old.addr)
		}()
	}
	return g.gen, nil
}

func (s *Supervisor) startGenerationLocked(ln net.Listener) *generation {
	s.gen++
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 0,
		MaxHeaderBytes:    1 << 20,
	}
	g := &generation{server: srv, ln: ln, addr: ln.Addr().String(), gen: s.gen}
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			s.logger.Warn("listener stopped", "generation", g.gen, "error", err)
		}
	}()
	return g
}

// Addr returns the current bound address.
func (s *Supervisor) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ""
	}
	return s.current.addr
}

// Generation returns the current listener generation.
func (s *Supervisor) Generation() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// Shutdown stops the current listener and waits for active requests up to the
// context deadline.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	g := s.current
	s.current = nil
	s.mu.Unlock()
	if g == nil {
		return nil
	}
	return g.server.Shutdown(ctx)
}
