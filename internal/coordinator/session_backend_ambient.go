package coordinator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Compile-time interface compliance checks.
var _ SessionBackend   = (*AmbientSessionBackend)(nil)
var _ SessionLifecycle = (*AmbientSessionBackend)(nil)
var _ SessionObserver  = (*AmbientSessionBackend)(nil)
var _ SessionActor     = (*AmbientSessionBackend)(nil)

// AmbientSessionBackend implements SessionBackend using the ACP backend API directly.
type AmbientSessionBackend struct {
	apiURL     string // e.g. "https://backend-route-ambient-code.apps.okd1.timslab"
	token      string // Bearer token
	project    string // project slug used in URL path
	timeout    int    // session timeout in seconds (default 900)
	httpClient *http.Client

	workflowURL    string // default workflow git URL
	workflowBranch string // default workflow branch
	workflowPath   string // default workflow path within repo
	coordinatorURL string // external coordinator URL for BOSS_URL env var

	availMu     sync.Mutex
	availCached bool
	availAt     time.Time
}

// AmbientBackendConfig holds configuration for creating an AmbientSessionBackend.
type AmbientBackendConfig struct {
	APIURL             string
	Token              string
	Project            string
	Timeout            int    // session timeout in seconds; 0 defaults to 900
	SkipTLSVerify      bool
	WorkflowURL        string // Git URL of workflow repo
	WorkflowBranch     string // Branch (optional, defaults to main)
	WorkflowPath       string // Path within repo to workflow dir
	CoordinatorExternalURL string // e.g. "https://jsell-agent-boss.apps.okd1.timslab"
}

// NewAmbientSessionBackend creates an AmbientSessionBackend from the given config.
func NewAmbientSessionBackend(cfg AmbientBackendConfig) *AmbientSessionBackend {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 900
	}
	return &AmbientSessionBackend{
		apiURL:         strings.TrimRight(cfg.APIURL, "/"),
		token:          cfg.Token,
		project:        cfg.Project,
		timeout:        timeout,
		workflowURL:    cfg.WorkflowURL,
		workflowBranch: cfg.WorkflowBranch,
		workflowPath:   cfg.WorkflowPath,
		coordinatorURL: cfg.CoordinatorExternalURL,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}
}

func (b *AmbientSessionBackend) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, b.apiURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return b.httpClient.Do(req)
}

// --- Identity ---

func (b *AmbientSessionBackend) Name() string { return "ambient" }

func (b *AmbientSessionBackend) Available() bool {
	b.availMu.Lock()
	if time.Since(b.availAt) < 30*time.Second {
		cached := b.availCached
		b.availMu.Unlock()
		return cached
	}
	b.availMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionsPath(), nil)
	if err != nil {
		b.setCachedAvail(false)
		return false
	}
	resp.Body.Close()

	// Any 2xx/4xx means the API is reachable.
	avail := resp.StatusCode < 500
	b.setCachedAvail(avail)
	return avail
}

func (b *AmbientSessionBackend) setCachedAvail(v bool) {
	b.availMu.Lock()
	b.availCached = v
	b.availAt = time.Now()
	b.availMu.Unlock()
}

// --- Lifecycle ---

func (b *AmbientSessionBackend) CreateSession(ctx context.Context, opts SessionCreateOpts) (string, error) {
	initialPrompt := opts.Command
	if initialPrompt == "" {
		initialPrompt = "You are an agent. Await instructions."
	}
	body := map[string]interface{}{
		"initialPrompt": initialPrompt,
		"runnerType":    "claude-agent-sdk",
		"timeout":       b.timeout,
	}

	// Determine workflow: per-session overrides backend default.
	var wf *WorkflowRef
	if ao, ok := opts.BackendOpts.(AmbientCreateOpts); ok {
		if ao.DisplayName != "" {
			body["displayName"] = ao.DisplayName
		} else if opts.SessionID != "" {
			body["displayName"] = opts.SessionID
		}
		if ao.Model != "" {
			body["llmSettings"] = map[string]string{"model": ao.Model}
		}
		if len(ao.Repos) > 0 {
			body["repos"] = ao.Repos
		}
		if ao.Workflow != nil {
			wf = ao.Workflow
		}
		// Labels for session discovery and ownership tracking.
		labels := map[string]string{"managed-by": "agent-boss"}
		if ao.SpaceName != "" {
			if !validLabelValue(ao.SpaceName) {
				return "", fmt.Errorf("create session: space name %q is not a valid Kubernetes label value (must be alphanumeric, '-', '_', or '.', max 63 chars, no spaces)", ao.SpaceName)
			}
			labels["boss-space"] = ao.SpaceName
		}
		if ao.DisplayName != "" {
			if !validLabelValue(ao.DisplayName) {
				return "", fmt.Errorf("create session: agent name %q is not a valid Kubernetes label value (must be alphanumeric, '-', '_', or '.', max 63 chars, no spaces)", ao.DisplayName)
			}
			labels["boss-agent"] = ao.DisplayName
		}
		body["labels"] = labels
	} else if opts.SessionID != "" {
		body["displayName"] = opts.SessionID
	}

	// Apply workflow (per-session override > backend default).
	if wf == nil && b.workflowURL != "" {
		wf = &WorkflowRef{
			GitURL: b.workflowURL,
			Branch: b.workflowBranch,
			Path:   b.workflowPath,
		}
	}
	if wf != nil {
		wfMap := map[string]string{"gitUrl": wf.GitURL}
		if wf.Branch != "" {
			wfMap["branch"] = wf.Branch
		}
		if wf.Path != "" {
			wfMap["path"] = wf.Path
		}
		body["activeWorkflow"] = wfMap
	}

	// Build environment variables: backend defaults first, then per-session overrides.
	// The backend API rejects env var values containing "://" (URL scheme),
	// so we split URL values into _SCHEME + _HOST parts for reassembly by the agent.
	envVars := make(map[string]string)
	if b.coordinatorURL != "" {
		splitEnvURL(envVars, "BOSS_URL", b.coordinatorURL)
	}
	if opts.SessionID != "" {
		envVars["AGENT_NAME"] = opts.SessionID
	}
	if ao, ok := opts.BackendOpts.(AmbientCreateOpts); ok {
		for k, v := range ao.EnvVars {
			if strings.Contains(v, "://") {
				splitEnvURL(envVars, k, v)
			} else {
				envVars[k] = v
			}
		}
	}
	if len(envVars) > 0 {
		body["envVars"] = envVars
	}

	resp, err := b.doRequest(ctx, http.MethodPost, b.sessionsPath(), body)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("create session: HTTP %d: %s", resp.StatusCode, string(msg))
	}

	var result struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}
	return result.Name, nil
}

func (b *AmbientSessionBackend) KillSession(ctx context.Context, sessionID string) error {
	resp, err := b.doRequest(ctx, http.MethodDelete, b.sessionPath(sessionID), nil)
	if err != nil {
		return fmt.Errorf("kill session: %w", err)
	}
	resp.Body.Close()

	// Any 2xx (200, 204) or 404 (already gone) are all success.
	if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("kill session: HTTP %d", resp.StatusCode)
}

func (b *AmbientSessionBackend) SessionExists(sessionID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionPath(sessionID), nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (b *AmbientSessionBackend) ListSessions() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionsPath(), nil)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list sessions: HTTP %d", resp.StatusCode)
	}

	var list backendSessionList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode session list: %w", err)
	}

	ids := make([]string, len(list.Items))
	for i, s := range list.Items {
		ids[i] = s.Metadata.Name
	}
	return ids, nil
}

// --- Status ---

func (b *AmbientSessionBackend) GetStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionPath(sessionID), nil)
	if err != nil {
		return SessionStatusUnknown, fmt.Errorf("get session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return SessionStatusMissing, nil
	}
	if resp.StatusCode != http.StatusOK {
		return SessionStatusUnknown, fmt.Errorf("get session: HTTP %d", resp.StatusCode)
	}

	var cr backendSessionCR
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return SessionStatusUnknown, fmt.Errorf("decode session: %w", err)
	}

	switch cr.phase() {
	case "pending":
		return SessionStatusPending, nil
	case "completed":
		return SessionStatusCompleted, nil
	case "failed":
		return SessionStatusFailed, nil
	case "running":
		return SessionStatusRunning, nil
	default:
		return SessionStatusUnknown, nil
	}
}

// --- Observability ---

func (b *AmbientSessionBackend) IsIdle(sessionID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, _ := b.GetStatus(ctx, sessionID)
	return status == SessionStatusIdle
}

// CaptureOutput fetches session transcript via the backend /export endpoint.
//
// Limitation: the backend API has no lightweight transcript endpoint. /export
// returns the full aguiEvents payload (85KB+ for long sessions) and we parse
// it client-side to extract the last MESSAGES_SNAPSHOT. The old public API
// planned a server-side format=transcript param but it was never shipped.
// If this becomes a bottleneck (many agents polled frequently), the backend
// team would need to add a filtered endpoint or a query param on /export.
func (b *AmbientSessionBackend) CaptureOutput(sessionID string, lines int) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionPath(sessionID)+"/export", nil)
	if err != nil {
		return nil, fmt.Errorf("capture output: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("capture output: HTTP %d", resp.StatusCode)
	}

	var export ambientExportResponse
	if err := json.NewDecoder(resp.Body).Decode(&export); err != nil {
		return nil, fmt.Errorf("decode export: %w", err)
	}

	// Prefer legacyMessages; fall back to the last MESSAGES_SNAPSHOT in aguiEvents.
	messages := export.LegacyMessages
	if len(messages) == 0 {
		messages = lastMessagesSnapshot(export.AguiEvents)
	}

	var result []string
	for _, msg := range messages {
		content := msg.Content
		if len(content) > 200 {
			content = content[:197] + "..."
		}
		result = append(result, fmt.Sprintf("[%s] %s", msg.Role, content))
	}

	if lines > 0 && len(result) > lines {
		result = result[len(result)-lines:]
	}
	return result, nil
}

func (b *AmbientSessionBackend) CheckApproval(sessionID string) ApprovalInfo {
	return ApprovalInfo{NeedsApproval: false}
}

// --- Interaction ---

func (b *AmbientSessionBackend) SendInput(sessionID string, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := map[string]interface{}{
		"messages": []map[string]string{
			{
				"id":      generateMsgID(),
				"role":    "user",
				"content": text,
			},
		},
	}
	resp, err := b.doRequest(ctx, http.MethodPost, b.sessionPath(sessionID)+"/agui/run", body)
	if err != nil {
		return fmt.Errorf("send input: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("send input: HTTP %d", resp.StatusCode)
}

func (b *AmbientSessionBackend) Approve(sessionID string) error {
	return nil // Ambient sessions don't have terminal approval prompts
}

func (b *AmbientSessionBackend) AlwaysAllow(sessionID string) error {
	return nil // Ambient sessions don't have terminal approval prompts
}

func (b *AmbientSessionBackend) Interrupt(ctx context.Context, sessionID string) error {
	resp, err := b.doRequest(ctx, http.MethodPost, b.sessionPath(sessionID)+"/agui/interrupt", nil)
	if err != nil {
		return fmt.Errorf("interrupt session: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("interrupt session: HTTP %d", resp.StatusCode)
}

// --- Discovery ---

func (b *AmbientSessionBackend) DiscoverSessions() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := b.doRequest(ctx, http.MethodGet, b.sessionsPath(), nil)
	if err != nil {
		return nil, fmt.Errorf("discover sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover sessions: HTTP %d", resp.StatusCode)
	}

	var list backendSessionList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode session list: %w", err)
	}

	discovered := make(map[string]string)
	for _, cr := range list.Items {
		phase := cr.phase()
		if phase != "running" && phase != "pending" {
			continue
		}
		// Prefer label-based matching, fall back to spec.displayName.
		// Sessions without either are unmanaged and skipped.
		name := cr.Metadata.Labels["boss-agent"]
		if name == "" {
			name = cr.Spec.DisplayName
		}
		if name != "" {
			discovered[name] = cr.Metadata.Name
		}
	}
	return discovered, nil
}

// --- Polling helpers ---

// waitForRunning polls GetStatus until the session reaches running or idle state.
// Used after CreateSession since ambient session creation is asynchronous.
func (b *AmbientSessionBackend) waitForRunning(ctx context.Context, sessionID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, _ := b.GetStatus(ctx, sessionID)
		if status == SessionStatusRunning || status == SessionStatusIdle {
			return nil
		}
		if status == SessionStatusFailed {
			return fmt.Errorf("session %s failed to start", sessionID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timed out after %s waiting for session %s to start", timeout, sessionID)
}
