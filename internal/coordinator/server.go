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
	sqliteadapter "github.com/ambient/platform/components/boss/internal/adapters/storage/sqlite"
	"github.com/ambient/platform/components/boss/internal/domain/ports"
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
	host            string // hostname used in URLs sent to agents (COORDINATOR_HOST, default "localhost")
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
	journal        *EventJournal
	repo           *bossdb.Repository // nil until Start() initialises the DB
	storage        ports.StoragePort  // domain-layer storage port; wired in Start()
	backends       map[string]SessionBackend
	defaultBackend string
	logger               Logger
	// allowSkipPermissions, when true, appends --dangerously-skip-permissions to every
	// tmux-backend agent launch command. Controlled by the BOSS_ALLOW_SKIP_PERMISSIONS
	// environment variable (default off). This is a deliberate operator-level toggle —
	// one decision applies to all agents uniformly.
	allowSkipPermissions bool
	personas             *PersonaStore
	// spawnInProgress serializes concurrent spawn requests for the same agent.
	// Keyed by "space/agent" (lowercase). LoadOrStore guards the TOCTOU window
	// between SessionExists() and CreateSession().
	spawnInProgress sync.Map
	// snapCache tracks the last-written snapshot value per "space/agent" key.
	// Used by appendSnapshot to skip DB writes when status+stale is unchanged,
	// preventing the 37K rows/day unbounded growth of status_snapshots.
	snapCache sync.Map
	// apiToken is the value of BOSS_API_TOKEN. When non-empty, all mutating
	// HTTP requests must carry "Authorization: Bearer <token>". When empty the
	// server runs in open mode (backward compatible for local development).
	apiToken string
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
	host := os.Getenv("COORDINATOR_HOST")
	if host == "" {
		host = "localhost"
	}
	s := &Server{
		port:               port,
		host:               host,
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
		journal:              NewEventJournal(dataDir),
		backends:             map[string]SessionBackend{"tmux": NewTmuxSessionBackend()},
		defaultBackend:       "tmux",
		logger:               NewLogger(os.Stdout),
		allowSkipPermissions: os.Getenv("BOSS_ALLOW_SKIP_PERMISSIONS") == "true",
		personas:             nil, // initialized in Start() after DB is ready
		apiToken:             os.Getenv("BOSS_API_TOKEN"),
	}

	if apiURL := os.Getenv("AMBIENT_API_URL"); apiURL != "" {
		skipTLS := os.Getenv("AMBIENT_SKIP_TLS_VERIFY") == "true"
		s.backends["ambient"] = NewAmbientSessionBackend(AmbientBackendConfig{
			APIURL:                 apiURL,
			Token:                  os.Getenv("AMBIENT_TOKEN"),
			Project:                os.Getenv("AMBIENT_PROJECT"),
			SkipTLSVerify:          skipTLS,
			WorkflowURL:            os.Getenv("AMBIENT_WORKFLOW_URL"),
			WorkflowBranch:         os.Getenv("AMBIENT_WORKFLOW_BRANCH"),
			WorkflowPath:           os.Getenv("AMBIENT_WORKFLOW_PATH"),
			CoordinatorExternalURL: os.Getenv("COORDINATOR_EXTERNAL_URL"),
		})
		if !s.backends["tmux"].Available() {
			s.defaultBackend = "ambient"
		}
	}

	return s
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

// backendByName returns the SessionBackend registered under name.
// An empty name selects the server default. A non-empty name that is not
// registered returns a descriptive error instead of silently falling back,
// so callers can surface a clear HTTP response to the client.
func (s *Server) backendByName(name string) (SessionBackend, error) {
	if name == "" {
		return s.backends[s.defaultBackend], nil
	}
	if b, ok := s.backends[name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("backend %q is not configured on this server", name)
}

// backendFor returns the SessionBackend for the given agent.
// Dispatches based on the agent's BackendType field, falling back
// to the default backend if empty or unknown.
func (s *Server) backendFor(agent *AgentUpdate) SessionBackend {
	if agent != nil && agent.BackendType != "" {
		if b, ok := s.backends[agent.BackendType]; ok {
			return b
		}
	}
	return s.backends[s.defaultBackend]
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
	// Migrate personas from JSON file to SQLite on first run.
	if err := s.repo.ImportPersonasFromJSON(s.dataDir); err != nil {
		return fmt.Errorf("migrate personas: %w", err)
	}
	s.personas = newPersonaStore(s.repo)
	// Wire the domain StoragePort adapter over the GORM repository.
	s.storage = sqliteadapter.New(s.repo)
	// With SQLite as the primary store, switch the event journal to in-memory
	// ring buffer mode so no .events.jsonl files are written to disk.
	// Each event is also persisted to SQLite for cross-restart durability.
	repo := s.repo
	s.journal.UseRingBuffer(ringBufferCap, func(ev *SpaceEvent) {
		repo.AppendSpaceEvent(&bossdb.SpaceEventLog{
			ID:        ev.ID,
			SpaceName: ev.Space,
			EventType: string(ev.Type),
			Agent:     ev.Agent,
			Payload:   string(ev.Payload),
			Timestamp: ev.Timestamp,
		})
	})

	// Route InterruptLedger operations to SQLite.
	s.interrupts.SetRepo(s.repo)
	s.loadSettings() // load persisted settings before spaces

	if err := s.loadAllSpaces(); err != nil {
		return fmt.Errorf("load spaces: %w", err)
	}

	// Pre-warm the ring buffer from SQLite so event history survives restarts.
	s.loadEventRingFromDB()

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
	mux.HandleFunc("/personas", s.handlePersonaList)
	mux.HandleFunc("/personas/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/personas/")
		rest = strings.TrimRight(rest, "/")
		// Sub-routes: /personas/{id}/history, /personas/{id}/agents, etc.
		if idx := strings.Index(rest, "/"); idx >= 0 {
			id := rest[:idx]
			sub := rest[idx+1:]
			switch sub {
			case "history":
				s.handlePersonaHistory(w, r, id)
			case "revert":
				s.handlePersonaRevert(w, r, id)
			case "agents":
				s.handlePersonaAgents(w, r, id)
			case "restart-outdated":
				s.handlePersonaRestartOutdated(w, r, id)
			default:
				writeJSONError(w, "not found", http.StatusNotFound)
			}
			return
		}
		s.handlePersonaDetail(w, r, rest)
	})
	mux.HandleFunc("/settings", s.handleSettings)
	mcpHandler := s.buildMCPHandler()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	listener, err := net.Listen("tcp", s.port)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.port, err)
	}
	s.port = ":" + strings.Split(listener.Addr().String(), ":")[len(strings.Split(listener.Addr().String(), ":"))-1]

	s.httpServer = &http.Server{Handler: s.requestLoggingMiddleware(securityHeadersMiddleware(s.authMiddleware(mux)))}
	s.running = true

	go func() {
		s.emit(DomainEvent{Level: LevelInfo, EventType: EventServerStarted,
			Msg:    fmt.Sprintf("coordinator started on %s (data: %s)", s.port, s.dataDir),
			Fields: map[string]string{"port": s.port, "data_dir": s.dataDir}})
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.emit(DomainEvent{Level: LevelError, EventType: EventServerError,
				Msg: fmt.Sprintf("server error: %v", err)})
		}
	}()

	go s.livenessLoop()
	if s.repo != nil {
		// Prune status_snapshots older than 7 days every 6 hours.
		// This caps the table that was growing at 37K rows/day.
		go s.snapshotPruneLoop(6*time.Hour, 7*24*time.Hour)
	}
	// Compaction rewrites .events.jsonl files; skip it when SQLite is active.
	if s.repo == nil {
		s.startCompactionLoop(30 * time.Minute)
	}

	return nil
}

// localURL returns the base URL of this server (e.g. "http://localhost:8899").
// The hostname comes from COORDINATOR_HOST (default "localhost").
func (s *Server) localURL() string {
	port := strings.TrimPrefix(s.port, ":")
	return "http://" + s.host + ":" + port
}

// mcpServerName returns a unique MCP server name for this instance.
// Default (localhost:8899) → "boss-mcp" (unchanged for backwards compat).
// Non-default port on localhost → "boss-mcp-{port}".
// Non-localhost host → "boss-mcp-{host}-{port}".
func (s *Server) mcpServerName() string {
	port := strings.TrimPrefix(s.port, ":")
	isDefaultHost := s.host == "localhost" || s.host == "127.0.0.1" || s.host == ""
	if isDefaultHost && port == "8899" {
		return "boss-mcp"
	}
	if isDefaultHost {
		return "boss-mcp-" + port
	}
	return "boss-mcp-" + s.host + "-" + port
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
	s.emit(DomainEvent{Level: LevelInfo, EventType: EventServerStopped, Msg: "coordinator stopped"})
	return err
}
