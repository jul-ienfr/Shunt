package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"shunt/internal/config"
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

// Retire stops accepting new connections while letting in-flight requests
// finish in the background.
func (s *Supervisor) Retire() {
	s.mu.Lock()
	g := s.current
	s.current = nil
	s.mu.Unlock()
	if g == nil {
		return
	}
	_ = g.ln.Close()
	go func() {
		_ = g.server.Shutdown(context.Background())
		s.logger.Info("listener generation retired", "generation", g.gen, "listen", g.addr)
	}()
}

// ListenerSet owns one Supervisor per named entry port. It lets the relay
// expose several local ports, each routed to a pinned profile or the global
// selection, without restarting the process.
type ListenerSet struct {
	mu      sync.Mutex
	logger  *slog.Logger
	handler func(name string) http.Handler
	items   map[string]*listenerBinding
	order   []string
}

type listenerBinding struct {
	supervisor *Supervisor
	profile    string
	listen     string
}

// NewListenerSet creates an empty set. handler is called once per listener name
// and must return a stable handler that resolves the pin at request time.
func NewListenerSet(handler func(name string) http.Handler, logger *slog.Logger) *ListenerSet {
	if logger == nil {
		logger = slog.Default()
	}
	return &ListenerSet{
		logger:  logger,
		handler: handler,
		items:   map[string]*listenerBinding{},
	}
}

// Start binds every listener. On failure nothing keeps serving, so a bad
// configuration cannot leave a half-open entry port.
func (l *ListenerSet) Start(listeners []config.Listener) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.items) != 0 {
		return fmt.Errorf("listener set already started")
	}
	items := map[string]*listenerBinding{}
	order := make([]string, 0, len(listeners))
	for _, spec := range listeners {
		sup := NewSupervisor(l.handler(spec.Name), l.logger)
		if err := sup.Start(spec.Listen); err != nil {
			for _, started := range items {
				_ = started.supervisor.Shutdown(context.Background())
			}
			return fmt.Errorf("bind %s (%s): %w", spec.Listen, spec.Name, err)
		}
		items[spec.Name] = &listenerBinding{supervisor: sup, profile: spec.Profile, listen: spec.Listen}
		order = append(order, spec.Name)
	}
	l.items = items
	l.order = order
	return nil
}

// Apply reconciles the running set with the configuration. New addresses are
// bound before old ones are retired; on failure the previous set keeps serving.
func (l *ListenerSet) Apply(listeners []config.Listener) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	desired := make(map[string]config.Listener, len(listeners))
	order := make([]string, 0, len(listeners))
	for _, spec := range listeners {
		desired[spec.Name] = spec
		order = append(order, spec.Name)
	}
	pending := map[string]*listenerBinding{}
	added := make([]*Supervisor, 0, len(listeners))
	for name, spec := range desired {
		existing, ok := l.items[name]
		if ok && existing.listen == spec.Listen {
			existing.profile = spec.Profile
			continue
		}
		sup := NewSupervisor(l.handler(name), l.logger)
		if err := sup.Start(spec.Listen); err != nil {
			for _, started := range added {
				started.Retire()
			}
			return fmt.Errorf("bind %s (%s): %w", spec.Listen, name, err)
		}
		added = append(added, sup)
		pending[name] = &listenerBinding{supervisor: sup, profile: spec.Profile, listen: spec.Listen}
	}
	for name, existing := range l.items {
		if _, keep := desired[name]; !keep {
			existing.supervisor.Retire()
		}
	}
	for name, binding := range pending {
		if existing, ok := l.items[name]; ok && existing.supervisor != binding.supervisor {
			existing.supervisor.Retire()
		}
		l.items[name] = binding
	}
	l.order = order
	return nil
}

// Rebind moves one named listener to a new address.
func (l *ListenerSet) Rebind(name, addr string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	binding, ok := l.items[name]
	if !ok {
		return 0, fmt.Errorf("listener %q is not running", name)
	}
	gen, err := binding.supervisor.Rebind(addr)
	if err != nil {
		return 0, err
	}
	binding.listen = addr
	return gen, nil
}

// Pinned returns the profile pinned to a listener, or "" when it follows the
// global selection.
func (l *ListenerSet) Pinned(name string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if binding, ok := l.items[name]; ok {
		return binding.profile
	}
	return ""
}

// Addr returns the bound address for one listener.
func (l *ListenerSet) Addr(name string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if binding, ok := l.items[name]; ok {
		return binding.supervisor.Addr()
	}
	return ""
}

// Primary returns the first listener name, which is the entry used by status,
// doctor, and the GUI entry editor.
func (l *ListenerSet) Primary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.order) == 0 {
		return ""
	}
	return l.order[0]
}

// PrimaryAddr returns the bound address of the primary listener.
func (l *ListenerSet) PrimaryAddr() string {
	name := l.Primary()
	if name == "" {
		return ""
	}
	return l.Addr(name)
}

// Order returns the listener names in configuration order.
func (l *ListenerSet) Order() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.order...)
}

// Shutdown stops every listener and waits for active requests up to the
// context deadline.
func (l *ListenerSet) Shutdown(ctx context.Context) error {
	l.mu.Lock()
	items := l.items
	l.items = map[string]*listenerBinding{}
	l.order = nil
	l.mu.Unlock()
	var first error
	for _, binding := range items {
		if err := binding.supervisor.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
