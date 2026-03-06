package coordinator

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
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
	DefaultPort      = ":8899"
	DefaultSpaceName = "default"
)

//go:embed protocol.md
var protocolTemplate string

type sseClient struct {
	ch    chan []byte
	space string
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
	sseClients      map[*sseClient]struct{}
	sseMu           sync.Mutex
	interrupts      *InterruptLedger
	approvalTracked map[string]time.Time
	// nudgePending tracks agents that should be nudged (check-in triggered)
	// when they next become idle. Set when a message arrives for an agent.
	// The liveness loop picks it up on the next tick where the agent is idle.
	// Keyed by "space/agent".
	nudgePending  map[string]time.Time
	nudgeInFlight map[string]bool // prevents duplicate concurrent nudges
	nudgeMu       sync.Mutex
}

func NewServer(port, dataDir string) *Server {
	if port == "" {
		port = DefaultPort
	}
	return &Server{
		port:            port,
		dataDir:         dataDir,
		spaces:          make(map[string]*KnowledgeSpace),
		stopLiveness:    make(chan struct{}),
		sseClients:      make(map[*sseClient]struct{}),
		interrupts:      NewInterruptLedger(dataDir),
		approvalTracked: make(map[string]time.Time),
		nudgePending:    make(map[string]time.Time),
		nudgeInFlight:   make(map[string]bool),
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

func (s *Server) logEvent(msg string) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	s.EventLog = append(s.EventLog, entry)
	if len(s.EventLog) > 200 {
		s.EventLog = s.EventLog[len(s.EventLog)-200:]
	}
}

func (s *Server) RecentEvents(n int) []string {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if n > len(s.EventLog) {
		n = len(s.EventLog)
	}
	out := make([]string, n)
	copy(out, s.EventLog[len(s.EventLog)-n:])
	return out
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

	// Protocol template is now embedded at compile time

	if err := s.loadAllSpaces(); err != nil {
		return fmt.Errorf("load spaces: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/mc2", s.handleMC2)
	// Static file server for CSS, JS, etc. (legacy dashboard)
	mux.Handle("/css/", http.StripPrefix("/", http.FileServer(http.Dir("internal/coordinator/static"))))
	mux.Handle("/js/", http.StripPrefix("/", http.FileServer(http.Dir("internal/coordinator/static"))))

	// Serve Vue frontend assets when frontendDir is configured
	if s.frontendDir != "" {
		if info, err := os.Stat(s.frontendDir); err == nil && info.IsDir() {
			mux.Handle("/assets/", http.FileServer(http.Dir(s.frontendDir)))
		}
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

func (s *Server) spacePath(name string) string {
	return filepath.Join(s.dataDir, name+".json")
}

func (s *Server) spaceMarkdownPath(name string) string {
	return filepath.Join(s.dataDir, name+".md")
}

func (s *Server) loadAllSpaces() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		ks, err := s.loadSpace(name)
		if err != nil {
			s.logEvent(fmt.Sprintf("failed to load space %q: %v", name, err))
			continue
		}
		s.spaces[name] = ks
		s.logEvent(fmt.Sprintf("loaded space %q (%d agents)", name, len(ks.Agents)))
	}
	return nil
}

func (s *Server) loadSpace(name string) (*KnowledgeSpace, error) {
	data, err := os.ReadFile(s.spacePath(name))
	if err != nil {
		return nil, err
	}
	var ks KnowledgeSpace
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", name, err)
	}
	if ks.Agents == nil {
		ks.Agents = make(map[string]*AgentUpdate)
	}
	return &ks, nil
}

const maxBackups = 10

func (s *Server) rotateBackups(spaceName string) {
	backupDir := filepath.Join(s.dataDir, "backups")
	os.MkdirAll(backupDir, 0755)

	base := filepath.Join(backupDir, spaceName+".json")
	for i := maxBackups; i > 1; i-- {
		src := fmt.Sprintf("%s.%d", base, i-1)
		dst := fmt.Sprintf("%s.%d", base, i)
		os.Rename(src, dst)
	}

	src := s.spacePath(spaceName)
	dst := fmt.Sprintf("%s.%d", base, 1)
	data, err := os.ReadFile(src)
	if err == nil {
		os.WriteFile(dst, data, 0644)
	}
}

func (s *Server) saveSpace(ks *KnowledgeSpace) error {
	s.refreshProtocol(ks)
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", ks.Name, err)
	}
	s.rotateBackups(ks.Name)
	if err := os.WriteFile(s.spacePath(ks.Name), data, 0644); err != nil {
		return err
	}
	md := ks.RenderMarkdown()
	if err := os.WriteFile(s.spaceMarkdownPath(ks.Name), []byte(md), 0644); err != nil {
		s.logEvent(fmt.Sprintf("warning: failed to write markdown for %q: %v", ks.Name, err))
	}
	return nil
}

func (s *Server) refreshProtocol(ks *KnowledgeSpace) {
	if protocolTemplate == "" {
		return
	}
	// Only set protocol if SharedContracts is empty (don't overwrite manual edits)
	if ks.SharedContracts == "" {
		ks.SharedContracts = strings.ReplaceAll(protocolTemplate, "{SPACE}", ks.Name)
	}
}

func (s *Server) getOrCreateSpace(name string) *KnowledgeSpace {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ks, ok := s.spaces[name]; ok {
		return ks
	}
	ks := NewKnowledgeSpace(name)
	s.spaces[name] = ks
	s.logEvent(fmt.Sprintf("created space %q", name))
	return ks
}

func (s *Server) getSpace(name string) (*KnowledgeSpace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks, ok := s.spaces[name]
	return ks, ok
}

func (s *Server) listSpaceNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.spaces))
	for name := range s.spaces {
		names = append(names, name)
	}
	return names
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// Serve Vue SPA index.html when frontendDir is configured and exists
	if s.frontendDir != "" {
		indexPath := filepath.Join(s.frontendDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			content, err := os.ReadFile(indexPath)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content)
			return
		}
	}
	// Fallback to legacy dashboard
	s.serveHTMLFile(w, r, "mission-control.html")
}

func (s *Server) handleMC2(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mc2" {
		http.NotFound(w, r)
		return
	}
	s.serveHTMLFile(w, r, "mission-control2.html")
}

func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type spaceSummary struct {
		Name       string    `json:"name"`
		AgentCount int       `json:"agent_count"`
		CreatedAt  time.Time `json:"created_at"`
		UpdatedAt  time.Time `json:"updated_at"`
	}

	s.mu.RLock()
	summaries := make([]spaceSummary, 0, len(s.spaces))
	for _, ks := range s.spaces {
		summaries = append(summaries, spaceSummary{
			Name:       ks.Name,
			AgentCount: len(ks.Agents),
			CreatedAt:  ks.CreatedAt,
			UpdatedAt:  ks.UpdatedAt,
		})
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleSpaceRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/spaces/")
	parts := strings.Split(path, "/")

	spaceName := parts[0]
	if spaceName == "" {
		s.handleListSpaces(w, r)
		return
	}

	if len(parts) == 1 || (len(parts) == 2 && parts[1] == "") {
		if r.Method == http.MethodDelete {
			s.handleDeleteSpace(w, r, spaceName)
			return
		}
		s.handleSpaceView(w, r, spaceName)
		return
	}

	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	subRoute := parts[1]

	switch subRoute {
	case "events":
		s.handleSpaceSSE(w, r, spaceName)
	case "raw":
		s.handleSpaceRaw(w, r, spaceName)
	case "contracts":
		s.handleSpaceContracts(w, r, spaceName)
	case "archive":
		s.handleSpaceArchive(w, r, spaceName)
	case "agent":
		if len(parts) < 3 {
			http.Error(w, "missing agent name", http.StatusBadRequest)
			return
		}
		agentName := parts[2]
		if len(parts) >= 4 {
			// Handle sub-routes: /spaces/{space}/agent/{agent}/{action}
			action := strings.TrimRight(parts[3], "/")
			switch action {
			case "message":
				s.handleAgentMessage(w, r, spaceName, agentName)
			default:
				// Handle document path: /spaces/{space}/agent/{agent}/{slug}
				s.handleAgentDocument(w, r, spaceName, agentName, action)
			}
		} else {
			// Handle agent updates: /spaces/{space}/agent/{agent}
			agentName = strings.TrimRight(agentName, "/")
			s.handleSpaceAgent(w, r, spaceName, agentName)
		}
	case "api":
		if len(parts) == 3 {
			switch strings.TrimRight(parts[2], "/") {
			case "agents":
				s.handleSpaceAgentsJSON(w, r, spaceName)
			case "events":
				s.handleSpaceEventsJSON(w, r)
			case "tmux-status":
				s.handleSpaceTmuxStatus(w, r, spaceName)
			default:
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	case "ignition":
		agentName := ""
		if len(parts) == 3 {
			agentName = strings.TrimRight(parts[2], "/")
		}
		s.handleIgnition(w, r, spaceName, agentName)
	case "approve":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleApproveAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "broadcast":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleSingleBroadcast(w, r, spaceName, agentName)
		} else {
			s.handleBroadcast(w, r, spaceName)
		}
	case "reply":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleReplyAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "dismiss":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleDismissQuestion(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "factory":
		factorySub := ""
		if len(parts) == 3 {
			factorySub = strings.TrimRight(parts[2], "/")
		}
		switch factorySub {
		case "", "interrupts":
			s.handleInterrupts(w, r, spaceName)
		case "metrics":
			s.handleInterruptMetrics(w, r, spaceName)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSpaceView(w http.ResponseWriter, r *http.Request, spaceName string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.handleSpaceJSON(w, r, spaceName)
		return
	}
	s.serveHTMLFile(w, r, "mission-control.html")
}

func (s *Server) handleSpaceJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method == http.MethodDelete {
		s.handleDeleteSpace(w, r, spaceName)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks)
}

func (s *Server) handleDeleteSpace(w http.ResponseWriter, _ *http.Request, spaceName string) {
	s.mu.Lock()
	_, ok := s.spaces[spaceName]
	if !ok {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	delete(s.spaces, spaceName)
	s.mu.Unlock()

	os.Remove(s.spacePath(spaceName))
	os.Remove(s.spaceMarkdownPath(spaceName))

	s.logEvent(fmt.Sprintf("space %q deleted", spaceName))
	s.broadcastSSE(spaceName, "space_deleted", spaceName)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "deleted space %q", spaceName)
}

func (s *Server) handleSpaceRaw(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.RenderMarkdown())

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.SharedContracts = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] shared contracts updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceContracts(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.SharedContracts)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.SharedContracts = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] contracts updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceArchive(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.Archive)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.Archive = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] archive updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if agentName == "" {
		http.Error(w, "missing agent name", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		canonical := resolveAgentName(ks, agentName)
		s.mu.RLock()
		agent, exists := ks.Agents[canonical]
		s.mu.RUnlock()
		if !exists {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "{}")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent)

	case http.MethodPost:
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			http.Error(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			http.Error(w, fmt.Sprintf("agent %q cannot post to %q's channel", callerName, agentName), http.StatusForbidden)
			return
		}

		ks := s.getOrCreateSpace(spaceName)
		canonical := resolveAgentName(ks, agentName)

		contentType := r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var update AgentUpdate

		if strings.Contains(contentType, "application/json") {
			if err := json.Unmarshal(body, &update); err != nil {
				http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
				return
			}
		} else {
			update = AgentUpdate{
				Status:   StatusActive,
				Summary:  truncateLine(string(body), 120),
				FreeText: string(body),
			}
		}

		sanitizeAgentUpdate(&update)

		if err := update.Validate(); err != nil {
			http.Error(w, fmt.Sprintf("validation: %v", err), http.StatusBadRequest)
			return
		}

		update.UpdatedAt = time.Now().UTC()

		s.mu.Lock()
		if existing, ok := ks.Agents[canonical]; ok {
			if update.TmuxSession == "" && existing.TmuxSession != "" {
				update.TmuxSession = existing.TmuxSession
			}
			if update.RepoURL == "" && existing.RepoURL != "" {
				update.RepoURL = existing.RepoURL
			}
			// Preserve messages — agents don't include them in updates
			if len(update.Messages) == 0 && len(existing.Messages) > 0 {
				update.Messages = existing.Messages
			}
			// Preserve documents — managed via the /agent/{name}/{slug} endpoint
			if len(update.Documents) == 0 && len(existing.Documents) > 0 {
				update.Documents = existing.Documents
			}
		}
		ks.Agents[canonical] = &update
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] %s: %s", spaceName, canonical, update.Status, update.Summary))
		s.recordDecisionInterrupts(spaceName, canonical, &update)
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical, "status": string(update.Status), "summary": update.Summary})
		s.broadcastSSE(spaceName, "agent_updated", string(sseData))
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "accepted for [%s] in space %q", canonical, spaceName)

	case http.MethodDelete:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		canonical := resolveAgentName(ks, agentName)
		s.mu.Lock()
		delete(ks.Agents, canonical)
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s/%s] agent removed", spaceName, canonical))
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
		s.broadcastSSE(spaceName, "agent_removed", string(sseData))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "removed [%s] from space %q", canonical, spaceName)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentMessage(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentName = strings.TrimRight(agentName, "/")

	// Sender authentication - require X-Agent-Name header
	senderName := r.Header.Get("X-Agent-Name")
	if senderName == "" {
		http.Error(w, "missing X-Agent-Name header: sender must identify themselves", http.StatusBadRequest)
		return
	}

	var messageReq AgentMessage
	if err := json.NewDecoder(r.Body).Decode(&messageReq); err != nil {
		http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if strings.TrimSpace(messageReq.Message) == "" {
		http.Error(w, "message content is required", http.StatusBadRequest)
		return
	}

	// Sanitize and set message properties
	messageReq.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	messageReq.Message = strings.TrimSpace(messageReq.Message)
	messageReq.Sender = senderName
	messageReq.Timestamp = time.Now().UTC()

	ks, ok := s.getSpace(spaceName)
	if !ok {
		// Create space if it doesn't exist for messages
		ks = &KnowledgeSpace{
			Name:      spaceName,
			Agents:    make(map[string]*AgentUpdate),
			UpdatedAt: time.Now().UTC(),
		}
		s.mu.Lock()
		s.spaces[spaceName] = ks
		s.mu.Unlock()
	}

	canonical := resolveAgentName(ks, agentName)

	s.mu.Lock()
	agent, exists := ks.Agents[canonical]
	if !exists {
		// Create agent record if it doesn't exist
		agent = &AgentUpdate{
			Status:    StatusIdle,
			Summary:   fmt.Sprintf("%s: pending message delivery", canonical),
			Messages:  []AgentMessage{},
			UpdatedAt: time.Now().UTC(),
		}
		ks.Agents[canonical] = agent
	}

	// Add message to agent's inbox
	if agent.Messages == nil {
		agent.Messages = []AgentMessage{}
	}
	agent.Messages = append(agent.Messages, messageReq)

	// Limit message history to last 50 messages
	if len(agent.Messages) > 50 {
		agent.Messages = agent.Messages[len(agent.Messages)-50:]
	}

	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	// Log the message event
	s.logEvent(fmt.Sprintf("[%s/%s] Message from %s: %s", spaceName, canonical, senderName, messageReq.Message))

	// Broadcast SSE event for real-time updates
	sseData, _ := json.Marshal(map[string]interface{}{
		"space":   spaceName,
		"agent":   canonical,
		"sender":  senderName,
		"message": messageReq.Message,
	})
	s.broadcastSSE(spaceName, "agent_message", string(sseData))

	// Mark agent for nudge — the liveness loop will trigger a check-in
	// when the agent is next idle, so it reads the message via /raw.
	s.nudgeMu.Lock()
	s.nudgePending[spaceName+"/"+canonical] = time.Now()
	s.nudgeMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "delivered",
		"messageId": messageReq.ID,
		"recipient": canonical,
	})
}

func (s *Server) handleAgentDocument(w http.ResponseWriter, r *http.Request, spaceName, agentName, documentSlug string) {
	agentName = strings.TrimRight(agentName, "/")

	// Agent name enforcement - ensure X-Agent-Name header matches for writes
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			http.Error(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			http.Error(w, fmt.Sprintf("agent %q cannot post to %q's documents", callerName, agentName), http.StatusForbidden)
			return
		}
	}

	// Sanitize document slug
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(documentSlug) {
		http.Error(w, "invalid document slug: must be alphanumeric with underscores and dashes only", http.StatusBadRequest)
		return
	}

	// Create agent document directory
	agentDir := filepath.Join(s.dataDir, spaceName, agentName)
	docPath := filepath.Join(agentDir, documentSlug+".md")

	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(docPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "document not found", http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("read document: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Write(content)

	case http.MethodPost, http.MethodPut:
		contentType := r.Header.Get("Content-Type")
		if !strings.Contains(contentType, "text/markdown") && !strings.Contains(contentType, "text/plain") {
			http.Error(w, "Content-Type must be text/markdown or text/plain", http.StatusBadRequest)
			return
		}

		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Create agent directory if it doesn't exist
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			http.Error(w, fmt.Sprintf("create directory: %v", err), http.StatusInternalServerError)
			return
		}

		// Write document
		if err := os.WriteFile(docPath, content, 0644); err != nil {
			http.Error(w, fmt.Sprintf("write document: %v", err), http.StatusInternalServerError)
			return
		}

		// Update agent's documents list in the knowledge space
		ks := s.getOrCreateSpace(spaceName)
		canonical := resolveAgentName(ks, agentName)

		s.mu.Lock()
		if ks.Agents[canonical] == nil {
			ks.Agents[canonical] = &AgentUpdate{
				Status:    StatusActive,
				Summary:   "Document uploaded",
				UpdatedAt: time.Now().UTC(),
			}
		}

		agent := ks.Agents[canonical]

		// Add or update document in the list
		found := false
		for i, doc := range agent.Documents {
			if doc.Slug == documentSlug {
				agent.Documents[i].Content = string(content)
				found = true
				break
			}
		}
		if !found {
			agent.Documents = append(agent.Documents, AgentDocument{
				Slug:    documentSlug,
				Title:   documentSlug, // Default title is the slug, agents can override via JSON
				Content: string(content),
			})
		}

		agent.UpdatedAt = time.Now().UTC()
		ks.UpdatedAt = time.Now().UTC()

		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save space: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] document %q uploaded", spaceName, canonical, documentSlug))
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "document %q saved for [%s] in space %q", documentSlug, canonical, spaceName)

	case http.MethodDelete:
		if err := os.Remove(docPath); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "document not found", http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("delete document: %v", err), http.StatusInternalServerError)
			return
		}

		// Remove document from agent's list
		ks, ok := s.getSpace(spaceName)
		if ok {
			canonical := resolveAgentName(ks, agentName)
			s.mu.Lock()
			if agent := ks.Agents[canonical]; agent != nil {
				for i, doc := range agent.Documents {
					if doc.Slug == documentSlug {
						agent.Documents = append(agent.Documents[:i], agent.Documents[i+1:]...)
						break
					}
				}
				agent.UpdatedAt = time.Now().UTC()
				ks.UpdatedAt = time.Now().UTC()
				s.saveSpace(ks)
			}
			s.mu.Unlock()
		}

		s.logEvent(fmt.Sprintf("[%s/%s] document %q deleted", spaceName, agentName, documentSlug))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "document %q deleted", documentSlug)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleIgnition(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if agentName == "" {
		http.Error(w, "missing agent name: GET /spaces/{space}/ignition/{agent}", http.StatusBadRequest)
		return
	}

	tmuxSession := r.URL.Query().Get("tmux_session")

	ks := s.getOrCreateSpace(spaceName)

	if tmuxSession != "" {
		s.mu.Lock()
		canonical := resolveAgentName(ks, agentName)
		if existing, ok := ks.Agents[canonical]; ok {
			existing.TmuxSession = tmuxSession
		} else {
			ks.Agents[canonical] = &AgentUpdate{
				Status:      StatusIdle,
				Summary:     canonical + ": awaiting ignition",
				TmuxSession: tmuxSession,
				UpdatedAt:   time.Now().UTC(),
			}
		}
		ks.UpdatedAt = time.Now().UTC()
		s.saveSpace(ks)
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s/%s] tmux session registered via ignition: %s", spaceName, agentName, tmuxSession))
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Agent Ignition: %s\n\n", agentName))
	b.WriteString(fmt.Sprintf("You are **%s**, an autonomous AI agent working in workspace **%s**.\n\n", agentName, spaceName))

	b.WriteString("## Operating Mode\n\n")
	b.WriteString("**You are running autonomously. There is no human at this terminal.**\n\n")
	b.WriteString("- You do NOT have a conversational partner. Do not ask questions like \"Shall I...?\" or wait for confirmation.\n")
	b.WriteString("- Messages from other agents or the boss are **instructions to act on immediately**, not conversation starters.\n")
	b.WriteString("- Your ONLY means of communication is through `curl` commands to the coordinator API (described below).\n")
	b.WriteString("- When you receive a new task via messages, **start working on it immediately** — do not ask for permission.\n")
	b.WriteString("- If you need a decision from the boss, post a question tagged `[?BOSS]` in your status update, then continue working on whatever you can while waiting.\n")
	b.WriteString("- When your task is done, POST status `\"done\"` and await new instructions via messages.\n")
	b.WriteString("\n")

	b.WriteString("## Coordinator\n\n")
	b.WriteString(fmt.Sprintf("- Boss URL: `http://localhost%s`\n", s.port))
	b.WriteString(fmt.Sprintf("- Workspace: `%s`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Your channel: `POST /spaces/%s/agent/%s`\n", spaceName, agentName))
	b.WriteString(fmt.Sprintf("- Read blackboard: `GET /spaces/%s/raw`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Dashboard: `http://localhost%s/spaces/%s/`\n", s.port, spaceName))
	if tmuxSession != "" {
		b.WriteString(fmt.Sprintf("- Tmux session: `%s` (pre-registered)\n", tmuxSession))
	}
	b.WriteString("\n")

	b.WriteString("## Protocol\n\n")
	b.WriteString("1. **Read before write.** GET /raw first to see what others are doing.\n")
	b.WriteString(fmt.Sprintf("2. **Post to your channel only.** POST to `/spaces/%s/agent/%s` with `-H 'X-Agent-Name: %s'`.\n", spaceName, agentName, agentName))
	b.WriteString("3. **Tag questions** with `[?BOSS]` — they render highlighted in the dashboard.\n")
	b.WriteString("4. **Include location fields** in every POST: `branch`, `pr`, `test_count`.\n")
	if tmuxSession != "" {
		b.WriteString(fmt.Sprintf("5. **Tmux session is pre-registered.** Your session `%s` is already known to the coordinator. It is sticky — you do not need to include `tmux_session` in your POSTs.\n", tmuxSession))
	} else {
		b.WriteString("5. **Register your tmux session.** Include `\"tmux_session\"` in your first POST. Find it with `tmux display-message -p '#S'`. It is sticky — you only need to send it once.\n")
	}
	b.WriteString(fmt.Sprintf("6. **Check your messages.** When you read `/raw`, look for a `#### Messages` section under your agent name. Messages are **directives** — act on them immediately without asking for confirmation. To send a message to another agent: `curl -s -X POST http://localhost%s/spaces/%s/agent/{target}/message -H 'Content-Type: application/json' -H 'X-Agent-Name: %s' -d '{\"message\": \"...\"}'`\n", s.port, spaceName, agentName))
	b.WriteString("7. **Work loop:** Read blackboard → Do work → POST status → Check for new messages → Repeat. Do not stop and wait for human input.\n")
	b.WriteString("\n")

	b.WriteString("## Peer Agents\n\n")
	if len(ks.Agents) == 0 {
		b.WriteString("No agents have posted yet.\n\n")
	} else {
		b.WriteString("| Agent | Status | Summary |\n")
		b.WriteString("| ----- | ------ | ------- |\n")
		for name, agent := range ks.Agents {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", name, agent.Status, agent.Summary))
		}
		b.WriteString("\n")
	}

	canonical := resolveAgentName(ks, agentName)
	existing, hasExisting := ks.Agents[canonical]
	if hasExisting {
		b.WriteString("## Your Last State\n\n")
		b.WriteString(fmt.Sprintf("- Status: %s\n", existing.Status))
		b.WriteString(fmt.Sprintf("- Summary: %s\n", existing.Summary))
		if existing.Branch != "" {
			b.WriteString(fmt.Sprintf("- Branch: `%s`\n", existing.Branch))
		}
		if existing.PR != "" {
			b.WriteString(fmt.Sprintf("- PR: %s\n", existing.PR))
		}
		if existing.Phase != "" {
			b.WriteString(fmt.Sprintf("- Phase: %s\n", existing.Phase))
		}
		if existing.NextSteps != "" {
			b.WriteString(fmt.Sprintf("- Next steps: %s\n", existing.NextSteps))
		}
		b.WriteString("\n")

		if len(existing.Messages) > 0 {
			b.WriteString("## Pending Messages\n\n")
			b.WriteString("**You have unread messages. These are instructions — act on them immediately. Do not ask for confirmation.**\n\n")
			for _, msg := range existing.Messages {
				b.WriteString(fmt.Sprintf("- **%s** (%s): %s\n",
					msg.Sender, msg.Timestamp.Format("15:04"), msg.Message))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## JSON Post Template\n\n")
	b.WriteString("```bash\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST http://localhost%s/spaces/%s/agent/%s \\\n", s.port, spaceName, agentName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\n")
	b.WriteString("    \"status\": \"active\",\n")
	b.WriteString(fmt.Sprintf("    \"summary\": \"%s: working on ...\",\n", agentName))
	b.WriteString("    \"branch\": \"feat/...\",\n")
	b.WriteString("    \"items\": [\"...\"]\n")
	b.WriteString("  }'\n")
	b.WriteString("```\n")

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, b.String())
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commandType := r.URL.Query().Get("type")
	if commandType == "" {
		commandType = "check-in"
	}

	go func() {
		result := s.BroadcastCheckIn(spaceName, "", "")
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "broadcast (%s) initiated for space %q", commandType, spaceName)
}

func (s *Server) handleSingleBroadcast(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	commandType := r.URL.Query().Get("type")
	if commandType == "" {
		commandType = "check-in"
	}

	go func() {
		result := s.SingleAgentCheckIn(spaceName, agentName, "", "")
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "%s initiated for agent %q in space %q", commandType, agentName, spaceName)
}

func (s *Server) handleSpaceAgentsJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks.Agents)
}

func (s *Server) handleSpaceEventsJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	events := s.RecentEvents(50)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

type tmuxAgentStatus struct {
	Agent         string `json:"agent"`
	Session       string `json:"session"`
	Registered    bool   `json:"registered"`
	Exists        bool   `json:"exists"`
	Idle          bool   `json:"idle"`
	LastLine      string `json:"last_line,omitempty"`
	NeedsApproval bool   `json:"needs_approval"`
	ToolName      string `json:"tool_name,omitempty"`
	PromptText    string `json:"prompt_text,omitempty"`
}

func (s *Server) handleSpaceTmuxStatus(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	s.TmuxAutoDiscover(spaceName)

	s.mu.RLock()
	type agentSession struct {
		name    string
		session string
	}
	var pairs []agentSession
	for name, agent := range ks.Agents {
		pairs = append(pairs, agentSession{name: name, session: agent.TmuxSession})
	}
	s.mu.RUnlock()

	hasTmux := tmuxAvailable()
	var results []tmuxAgentStatus
	for i, p := range pairs {
		st := tmuxAgentStatus{
			Agent:      p.name,
			Session:    p.session,
			Registered: p.session != "",
		}
		if hasTmux && st.Registered {
			st.Exists = tmuxSessionExists(p.session)
			if st.Exists {
				st.Idle = tmuxIsIdle(p.session)
				st.LastLine, _ = tmuxCapturePaneLastLine(p.session)
				approval := tmuxCheckApproval(p.session)
				st.NeedsApproval = approval.NeedsApproval
				st.ToolName = approval.ToolName
				st.PromptText = approval.PromptText
			}
		}
		results = append(results, st)
		if hasTmux && i < len(pairs)-1 {
			time.Sleep(300 * time.Millisecond)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleApproveAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var tmuxSession string
	if exists {
		tmuxSession = agent.TmuxSession
	}
	s.mu.RUnlock()
	if !exists {
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if tmuxSession == "" {
		http.Error(w, canonical+": no tmux session registered", http.StatusBadRequest)
		return
	}
	if !tmuxSessionExists(tmuxSession) {
		http.Error(w, canonical+": tmux session not found", http.StatusBadRequest)
		return
	}
	approval := tmuxCheckApproval(tmuxSession)
	if !approval.NeedsApproval {
		http.Error(w, canonical+": not waiting for approval", http.StatusConflict)
		return
	}
	if err := tmuxApprove(tmuxSession); err != nil {
		http.Error(w, canonical+": approve failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logEvent(fmt.Sprintf("[%s/%s] approval granted via dashboard (tool: %s)", spaceName, canonical, approval.ToolName))
	key := spaceName + "/" + canonical
	ctx := map[string]string{"tool": approval.ToolName}
	if started, was := s.approvalTracked[key]; was {
		delete(s.approvalTracked, key)
		ctx["wait_seconds"] = fmt.Sprintf("%.1f", time.Since(started).Seconds())
	}
	s.interrupts.RecordResolved(spaceName, canonical, InterruptApproval,
		approval.PromptText, "human", "approved", ctx)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "approved", "agent": canonical, "tool": approval.ToolName})
}

func (s *Server) handleReplyAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var tmuxSession string
	if exists {
		tmuxSession = agent.TmuxSession
	}
	s.mu.RUnlock()
	if !exists {
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if tmuxSession == "" {
		http.Error(w, canonical+": no tmux session registered", http.StatusBadRequest)
		return
	}
	if !tmuxSessionExists(tmuxSession) {
		http.Error(w, canonical+": tmux session not found", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if err := tmuxSendKeys(tmuxSession, payload.Message); err != nil {
		http.Error(w, canonical+": send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logEvent(fmt.Sprintf("[%s/%s] boss reply sent via dashboard", spaceName, canonical))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "agent": canonical})
}

func (s *Server) handleDismissQuestion(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	if !exists {
		s.mu.Unlock()
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	switch payload.Type {
	case "question":
		if payload.Index < 0 || payload.Index >= len(agent.Questions) {
			s.mu.Unlock()
			http.Error(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Questions = append(agent.Questions[:payload.Index], agent.Questions[payload.Index+1:]...)
	case "blocker":
		if payload.Index < 0 || payload.Index >= len(agent.Blockers) {
			s.mu.Unlock()
			http.Error(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Blockers = append(agent.Blockers[:payload.Index], agent.Blockers[payload.Index+1:]...)
	default:
		s.mu.Unlock()
		http.Error(w, "type must be 'question' or 'blocker'", http.StatusBadRequest)
		return
	}
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] boss dismissed %s #%d via dashboard", spaceName, canonical, payload.Type, payload.Index))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, "agent_updated", string(sseData))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "dismissed", "agent": canonical})
}

func (s *Server) broadcastSSE(space, event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	payload := []byte(msg)
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for c := range s.sseClients {
		if c.space == "" || c.space == space {
			select {
			case c.ch <- payload:
			default:
			}
		}
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
}

func (s *Server) handleSpaceSSE(w http.ResponseWriter, r *http.Request, spaceName string) {
	s.serveSSE(w, r, spaceName)
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, space string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &sseClient{ch: make(chan []byte, 64), space: space}
	s.sseMu.Lock()
	s.sseClients[client] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, client)
		s.sseMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}

func (s *Server) livenessLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopLiveness:
			return
		case <-ticker.C:
			s.checkAllSessionLiveness()
		}
	}
}

func (s *Server) checkAllSessionLiveness() {
	if !tmuxAvailable() {
		return
	}
	s.mu.RLock()
	type probe struct {
		space, agent, session string
	}
	var probes []probe
	for spaceName, ks := range s.spaces {
		for name, agent := range ks.Agents {
			if agent.TmuxSession != "" {
				probes = append(probes, probe{spaceName, name, agent.TmuxSession})
			}
		}
	}
	s.mu.RUnlock()

	type statusEntry struct {
		agent, session   string
		exists, idle     bool
		needsApproval    bool
		toolName, prompt string
	}
	spaceResults := make(map[string][]statusEntry)
	for _, p := range probes {
		e := statusEntry{agent: p.agent, session: p.session}
		e.exists = tmuxSessionExists(p.session)
		if e.exists {
			e.idle = tmuxIsIdle(p.session)
			if !e.idle {
				approval := tmuxCheckApproval(p.session)
				e.needsApproval = approval.NeedsApproval
				e.toolName = approval.ToolName
				e.prompt = approval.PromptText
			}
		}
		spaceResults[p.space] = append(spaceResults[p.space], e)
	}

	for space, entries := range spaceResults {
		payload := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			key := space + "/" + e.agent
			if e.needsApproval {
				if _, already := s.approvalTracked[key]; !already {
					ctx := map[string]string{}
					if e.toolName != "" {
						ctx["tool"] = e.toolName
					}
					s.interrupts.Record(space, e.agent, InterruptApproval, e.prompt, ctx)
					s.approvalTracked[key] = time.Now()
					s.logEvent(fmt.Sprintf("[%s/%s] approval interrupt detected (tool: %s)", space, e.agent, e.toolName))
				}
			} else {
				if started, was := s.approvalTracked[key]; was {
					delete(s.approvalTracked, key)
					waitSec := time.Since(started).Seconds()
					s.interrupts.RecordResolved(space, e.agent, InterruptApproval,
						"", "auto", "cleared",
						map[string]string{"wait_seconds": fmt.Sprintf("%.1f", waitSec)})
					s.logEvent(fmt.Sprintf("[%s/%s] approval interrupt cleared (waited %.1fs)", space, e.agent, waitSec))
				}
			}

			m := map[string]interface{}{
				"agent":          e.agent,
				"session":        e.session,
				"exists":         e.exists,
				"idle":           e.idle,
				"needs_approval": e.needsApproval,
			}
			if e.toolName != "" {
				m["tool_name"] = e.toolName
			}
			if e.prompt != "" {
				m["prompt_text"] = e.prompt
			}
			payload[i] = m

			// Check if this idle agent has a pending nudge
			if e.exists && e.idle {
				s.nudgeMu.Lock()
				if _, pending := s.nudgePending[key]; pending {
					if !s.nudgeInFlight[key] {
						s.nudgeInFlight[key] = true
						delete(s.nudgePending, key)
						s.nudgeMu.Unlock()
						go s.executeNudge(space, e.agent)
					} else {
						s.nudgeMu.Unlock()
					}
				} else {
					s.nudgeMu.Unlock()
				}
			}
		}
		data, _ := json.Marshal(payload)
		s.broadcastSSE(space, "tmux_liveness", string(data))
	}
}

// executeNudge triggers a single-agent check-in for an agent that has
// pending messages. Called from the liveness loop when the agent is idle.
func (s *Server) executeNudge(spaceName, agentName string) {
	key := spaceName + "/" + agentName
	defer func() {
		s.nudgeMu.Lock()
		delete(s.nudgeInFlight, key)
		s.nudgeMu.Unlock()
	}()

	s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge: message pending, triggering check-in", spaceName, agentName))
	result := s.SingleAgentCheckIn(spaceName, agentName, "", "")

	if len(result.Errors) > 0 {
		s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge failed: %s", spaceName, agentName, result.Errors[0]))
	} else if len(result.Sent) > 0 {
		s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge complete", spaceName, agentName))
	}

	sseData, _ := json.Marshal(result)
	s.broadcastSSE(spaceName, "broadcast_complete", string(sseData))
}

func (s *Server) recordDecisionInterrupts(spaceName, agentName string, update *AgentUpdate) {
	for _, q := range update.Questions {
		ctx := map[string]string{}
		if update.Branch != "" {
			ctx["branch"] = update.Branch
		}
		if update.PR != "" {
			ctx["pr"] = update.PR
		}
		if update.Phase != "" {
			ctx["phase"] = update.Phase
		}
		s.interrupts.Record(spaceName, agentName, InterruptDecision, q, ctx)
	}
}

func (s *Server) handleInterrupts(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	interrupts := s.interrupts.LoadAll(spaceName)
	if interrupts == nil {
		interrupts = []Interrupt{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(interrupts)
}

func (s *Server) handleInterruptMetrics(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metrics := s.interrupts.Metrics(spaceName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func resolveAgentName(ks *KnowledgeSpace, raw string) string {
	for existing := range ks.Agents {
		if strings.EqualFold(existing, raw) {
			return existing
		}
	}
	return strings.ToUpper(raw[:1]) + strings.ToLower(raw[1:])
}

var devNullPattern = regexp.MustCompile(`\s*<\s*/dev/null\s*`)

func sanitizeInput(s string) string {
	return devNullPattern.ReplaceAllString(s, "")
}

func sanitizeAgentUpdate(u *AgentUpdate) {
	u.Summary = sanitizeInput(u.Summary)
	u.Phase = sanitizeInput(u.Phase)
	u.FreeText = sanitizeInput(u.FreeText)
	u.NextSteps = sanitizeInput(u.NextSteps)
	for i, item := range u.Items {
		u.Items[i] = sanitizeInput(item)
	}
	for i, q := range u.Questions {
		u.Questions[i] = sanitizeInput(q)
	}
	for i, b := range u.Blockers {
		u.Blockers[i] = sanitizeInput(b)
	}
	for si := range u.Sections {
		u.Sections[si].Title = sanitizeInput(u.Sections[si].Title)
		for i, item := range u.Sections[si].Items {
			u.Sections[si].Items[i] = sanitizeInput(item)
		}
	}
}

func truncateLine(s string, maxLen int) string {
	line := strings.SplitN(s, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > maxLen {
		return line[:maxLen-3] + "..."
	}
	return line
}

// serveHTMLFile serves an HTML file from the static directory
func (s *Server) serveHTMLFile(w http.ResponseWriter, r *http.Request, filename string) {
	filePath := filepath.Join("internal", "coordinator", "static", filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
