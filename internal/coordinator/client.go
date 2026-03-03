package coordinator

import (
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

	var agents map[string]*AgentUpdate
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

	resp, err := c.httpClient.Post(
		c.spacePrefix()+"/agent/"+name,
		"application/json",
		strings.NewReader(string(data)),
	)
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
	resp, err := c.httpClient.Do(req)
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

func (c *Client) DeleteSpace() error {
	req, err := http.NewRequest(http.MethodDelete, c.spacePrefix()+"/", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
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

func (c *Client) FetchIgnition(agentName string, tmuxSession string) (string, error) {
	url := c.spacePrefix() + "/ignition/" + agentName
	if tmuxSession != "" {
		url += "?tmux_session=" + tmuxSession
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
	resp, err := c.httpClient.Post(c.spacePrefix()+"/broadcast", "application/json", nil)
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
