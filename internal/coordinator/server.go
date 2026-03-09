package coordinator

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	bossdb "github.com/ambient/platform/components/boss/internal/coordinator/db"
)

const (
	DefaultPort      = ":8899"
	DefaultSpaceName = "default"
)


//go:embed protocol.md
var protocolTemplate string

type sseClient struct {
	ch    chan []byte
	space string
	agent string // if non-empty, only receive events targeted at this specific agent
}

// sseEvent holds a buffered SSE event for Last-Event-ID replay.
type sseEvent struct {
	ID        string
	EventType string
	Data      string
}

type Server struct {
	port            string
	dataDir         string
	frontendDir     string
	spaces          map[string]*KnowledgeSpace
	mu              sync.RWMutex
	httpServer      *http.Server
	running         bool
	runMu           sync.Mutex
	EventLog        []string
	eventMu         sync.Mutex
	stopLiveness    chan struct{}
	sseClients   map[*sseClient]struct{}
	sseMu        sync.Mutex
	agentSSEBuf  map[string][]sseEvent // keyed by "space/agent"; guarded by sseMu; ring buffer cap 200
	interrupts      *InterruptLedger
	approvalTracked map[string]time.Time
	// nudgePending tracks agents that should be nudged (check-in triggered)
	// when they next become idle. Set when a message arrives for an agent.
	// The liveness loop picks it up on the next tick where the agent is idle.
	// Keyed by "space/agent".
	nudgePending       map[string]time.Time
	nudgeInFlight      map[string]bool // prevents duplicate concurrent nudges
	nudgeMu            sync.Mutex
	stalenessThreshold time.Duration
	// registrations holds registration records for agents that have called /register.
	// Keyed by registrationKey(space, agent). Guarded by regMu.
	registrations map[string]*AgentRegistrationRecord
	regMu         sync.RWMutex
	journal       *EventJournal
	repo          *bossdb.Repository // nil until Start() initialises the DB
}

func NewServer(port, dataDir string) *Server {
	if port == "" {
		port = DefaultPort
	}
	thresh := StalenessThreshold
	if v := os.Getenv("STALENESS_THRESHOLD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			thresh = d
		}
	}
	return &Server{
		port:               port,
		dataDir:            dataDir,
		spaces:             make(map[string]*KnowledgeSpace),
		stopLiveness:       make(chan struct{}),
		sseClients:         make(map[*sseClient]struct{}),
		agentSSEBuf:        make(map[string][]sseEvent),
		interrupts:         NewInterruptLedger(dataDir),
		approvalTracked:    make(map[string]time.Time),
		nudgePending:       make(map[string]time.Time),
		nudgeInFlight:      make(map[string]bool),
		stalenessThreshold: thresh,
		registrations:      make(map[string]*AgentRegistrationRecord),
		journal:            NewEventJournal(dataDir),
	}
}

// SetFrontendDir configures the server to serve a Vue SPA from the given
// directory (typically frontend/dist). When set and the directory exists,
// the root "/" serves index.html from that directory and /assets/ serves
// Vite-built static files. When empty, the legacy mission-control.html is used.
func (s *Server) SetFrontendDir(dir string) {
	s.frontendDir = dir
}

func (s *Server) Running() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running
}

func (s *Server) Port() string {
	return s.port
}

func (s *Server) Start() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.running {
		return fmt.Errorf("already running")
	}

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// Initialise database repository.
	gdb, err := bossdb.Open(s.dataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	s.repo = bossdb.New(gdb)

	if err := s.loadAllSpaces(); err != nil {
		return fmt.Errorf("load spaces: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleRoot)
	// Serve Vue frontend assets: runtime FRONTEND_DIR overrides embedded assets.
	if s.frontendDir != "" {
		if info, err := os.Stat(s.frontendDir); err == nil && info.IsDir() {
			mux.Handle("/assets/", http.FileServer(http.Dir(s.frontendDir)))
		}
	} else if sub, err := fs.Sub(embeddedFrontend, "frontend"); err == nil {
		// Serve compiled Vue assets from the embedded filesystem.
		mux.Handle("/assets/", http.FileServer(http.FS(sub)))
	}
	mux.HandleFunc("/spaces", s.handleListSpaces)
	mux.HandleFunc("/spaces/", s.handleSpaceRoute)
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/raw", func(w http.ResponseWriter, r *http.Request) {
		s.handleSpaceRaw(w, r, DefaultSpaceName)
	})
	mux.HandleFunc("/agent/", func(w http.ResponseWriter, r *http.Request) {
		agentName := strings.TrimPrefix(r.URL.Path, "/agent/")
		agentName = strings.TrimRight(agentName, "/")
		s.handleSpaceAgent(w, r, DefaultSpaceName, agentName)
	})
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		s.handleSpaceAgentsJSON(w, r, DefaultSpaceName)
	})

	listener, err := net.Listen("tcp", s.port)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.port, err)
	}
	s.port = ":" + strings.Split(listener.Addr().String(), ":")[len(strings.Split(listener.Addr().String(), ":"))-1]

	s.httpServer = &http.Server{Handler: mux}
	s.running = true

	go func() {
		s.logEvent(fmt.Sprintf("coordinator started on %s (data: %s)", s.port, s.dataDir))
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logEvent(fmt.Sprintf("server error: %v", err))
		}
	}()

	go s.livenessLoop()
	s.startCompactionLoop(30 * time.Minute)

	return nil
}

func (s *Server) Stop() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if !s.running {
		return fmt.Errorf("not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	close(s.stopLiveness)
	err := s.httpServer.Shutdown(ctx)
	s.running = false
	s.logEvent("coordinator stopped")
	return err
}
