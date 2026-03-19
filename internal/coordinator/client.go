package coordinator

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	space      string
	authToken  string
	httpClient *http.Client
}

func NewClient(baseURL, space string) *Client {
	if space == "" {
		space = DefaultSpaceName
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		space:   space,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// WithAuthToken sets the Bearer token sent on all mutating (non-GET) requests.
// Returns the same client for chaining.
func (c *Client) WithAuthToken(token string) *Client {
	c.authToken = token
	return c
}

// WithInsecureTLS disables TLS certificate verification for connections to
// servers with self-signed certificates. Returns the same client for chaining.
func (c *Client) WithInsecureTLS() *Client {
	c.httpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — user-requested via --insecure flag
	}
	return c
}

// doRequest executes req, injecting Authorization: Bearer if authToken is set.
func (c *Client) doRequest(req *http.Request) (*http.Response, error) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	return c.httpClient.Do(req)
}

func (c *Client) spacePrefix() string {
	return c.baseURL + "/spaces/" + c.space
}

func (c *Client) FetchSpace() (*KnowledgeSpace, error) {
	resp, err := c.httpClient.Get(c.spacePrefix() + "/api/agents")
	if err != nil {
		return nil, fmt.Errorf("fetch agents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var agents map[string]*AgentRecord
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, fmt.Errorf("decode agents: %w", err)
	}

	return &KnowledgeSpace{
		Name:   c.space,
		Agents: agents,
	}, nil
}

func (c *Client) FetchMarkdown() (string, error) {
	resp, err := c.httpClient.Get(c.spacePrefix() + "/raw")
	if err != nil {
		return "", fmt.Errorf("fetch raw: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return string(body), nil
}

func (c *Client) FetchAgent(name string) (*AgentUpdate, error) {
	resp, err := c.httpClient.Get(c.spacePrefix() + "/agent/" + name)
	if err != nil {
		return nil, fmt.Errorf("fetch agent %s: %w", name, err)
	}
	defer resp.Body.Close()

	var agent AgentUpdate
	if err := json.NewDecoder(resp.Body).Decode(&agent); err != nil {
		return nil, fmt.Errorf("decode agent %s: %w", name, err)
	}
	return &agent, nil
}

func (c *Client) PostAgentUpdate(name string, update *AgentUpdate) error {
	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.spacePrefix()+"/agent/"+name, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", name)
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("post agent %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) DeleteAgent(name string) error {
	req, err := http.NewRequest(http.MethodDelete, c.spacePrefix()+"/agent/"+name, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("delete agent %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// RestartAgent calls POST /spaces/:space/agent/:name/restart, which kills the
// existing session and spawns a fresh one using the agent's stored config.
func (c *Client) RestartAgent(name string) error {
	req, err := http.NewRequest(http.MethodPost, c.spacePrefix()+"/agent/"+name+"/restart", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("restart agent %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// EnsureSpace creates the space if it does not already exist.
// Returns true if the space was newly created, false if it already existed.
func (c *Client) EnsureSpace() (created bool, err error) {
	// Try GET first — if the space exists, we're done.
	req, e := http.NewRequest(http.MethodGet, c.spacePrefix(), nil)
	if e != nil {
		return false, fmt.Errorf("create request: %w", e)
	}
	req.Header.Set("Accept", "application/json")
	resp, e := c.httpClient.Do(req)
	if e != nil {
		return false, fmt.Errorf("get space: %w", e)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return false, nil // already exists
	}

	// Space does not exist — POST to contracts creates it lazily.
	postReq, e := http.NewRequest(http.MethodPost, c.spacePrefix()+"/contracts", strings.NewReader(""))
	if e != nil {
		return false, fmt.Errorf("create request: %w", e)
	}
	postReq.Header.Set("Content-Type", "text/plain")
	postResp, e := c.doRequest(postReq)
	if e != nil {
		return false, fmt.Errorf("create space: %w", e)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(postResp.Body)
		return false, fmt.Errorf("create space: status %d: %s", postResp.StatusCode, string(body))
	}
	return true, nil
}

func (c *Client) DeleteSpace() error {
	req, err := http.NewRequest(http.MethodDelete, c.spacePrefix()+"/", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("delete space %s: %w", c.space, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) FetchIgnition(agentName string, sessionID string) (string, error) {
	url := c.spacePrefix() + "/ignition/" + agentName
	if sessionID != "" {
		url += "?session_id=" + sessionID
	}
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch ignition: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (c *Client) TriggerBroadcast() (string, error) {
	req, err := http.NewRequest(http.MethodPost, c.spacePrefix()+"/broadcast", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return "", fmt.Errorf("trigger broadcast: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

type SpaceSummary struct {
	Name       string    `json:"name"`
	AgentCount int       `json:"agent_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (c *Client) ListSpaces() ([]SpaceSummary, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/spaces")
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}
	defer resp.Body.Close()

	var summaries []SpaceSummary
	if err := json.NewDecoder(resp.Body).Decode(&summaries); err != nil {
		return nil, fmt.Errorf("decode spaces: %w", err)
	}
	return summaries, nil
}

// ─── Fleet / agent-compose client methods ────────────────────────────────────

// ExportFleet fetches the agent-compose YAML for the space.
func (c *Client) ExportFleet() ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.spacePrefix()+"/export", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("export fleet: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("export fleet: status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// SpaceExists returns true if the space is registered on the server.
func (c *Client) SpaceExists() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, c.spacePrefix(), nil)
	if err != nil {
		return false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("check space: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// FetchPersona returns the persona with the given ID, or (nil, nil) if not found.
func (c *Client) FetchPersona(id string) (*Persona, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/personas/" + id)
	if err != nil {
		return nil, fmt.Errorf("fetch persona %q: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch persona %q: status %d: %s", id, resp.StatusCode, string(body))
	}
	var p Persona
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode persona %q: %w", id, err)
	}
	return &p, nil
}

// CreatePersona creates a new global persona. A 409 Conflict from the server
// (ID already taken) is treated as a non-fatal condition; the returned persona
// body is decoded and returned.
func (c *Client) CreatePersona(p *Persona) (*Persona, error) {
	data, _ := json.Marshal(p)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/personas", strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, fmt.Errorf("create persona: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		return nil, fmt.Errorf("create persona: status %d: %s", resp.StatusCode, string(body))
	}
	var out Persona
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode persona response: %w", err)
	}
	return &out, nil
}

// UpdatePersona replaces the mutable fields of an existing persona.
func (c *Client) UpdatePersona(id, name, description, prompt string) error {
	payload := struct {
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
		Prompt      string `json:"prompt,omitempty"`
	}{name, description, prompt}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPatch, c.baseURL+"/personas/"+id, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("update persona: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update persona %q: status %d: %s", id, resp.StatusCode, string(body))
	}
	return nil
}

// FetchAgentConfig returns the agent's durable config, or an empty AgentConfig
// if the agent has none. Returns (nil, nil) if the space is not found (404).
func (c *Client) FetchAgentConfig(agentName string) (*AgentConfig, error) {
	resp, err := c.httpClient.Get(c.spacePrefix() + "/agent/" + agentName + "/config")
	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch config: status %d: %s", resp.StatusCode, string(body))
	}
	var cfg AgentConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &cfg, nil
}

// PatchAgentConfig performs a partial update of the agent's durable config.
// Sends X-Agent-Name header required by the server's channel enforcement.
func (c *Client) PatchAgentConfig(agentName string, cfg *AgentConfig) error {
	data, _ := json.Marshal(cfg)
	req, err := http.NewRequest(http.MethodPatch,
		c.spacePrefix()+"/agent/"+agentName+"/config",
		strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", agentName)
	resp, err := c.doRequest(req)
	if err != nil {
		return fmt.Errorf("patch config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch config: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
