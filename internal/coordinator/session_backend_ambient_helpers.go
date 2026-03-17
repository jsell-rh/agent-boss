package coordinator

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
)

// --- ACP backend API response types (K8s CR shape) ---

type backendSessionCR struct {
	Metadata struct {
		Name             string            `json:"name"`
		Labels           map[string]string `json:"labels,omitempty"`
		CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	} `json:"metadata"`
	Spec struct {
		DisplayName string `json:"displayName,omitempty"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase,omitempty"`
	} `json:"status"`
}

func (cr *backendSessionCR) displayName() string {
	if cr.Spec.DisplayName != "" {
		return cr.Spec.DisplayName
	}
	return cr.Metadata.Name
}

func (cr *backendSessionCR) phase() string {
	return strings.ToLower(cr.Status.Phase)
}

type ambientExportMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ambientExportResponse struct {
	LegacyMessages []ambientExportMessage `json:"legacyMessages"`
	AguiEvents     []json.RawMessage      `json:"aguiEvents"`
}

type aguiEvent struct {
	Type     string                `json:"type"`
	Messages []ambientExportMessage `json:"messages,omitempty"`
}

type backendSessionList struct {
	Items []backendSessionCR `json:"items"`
}

// --- Helpers ---

func (b *AmbientSessionBackend) sessionsPath() string {
	return "/api/projects/" + b.project + "/agentic-sessions"
}

func (b *AmbientSessionBackend) sessionPath(sessionID string) string {
	return b.sessionsPath() + "/" + sessionID
}

func generateMsgID() string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return fmt.Sprintf("%x", buf)
}

// splitEnvURL splits a URL value into scheme and host parts to avoid the
// backend API's rejection of env var values containing "://".
// For "https://example.com:8899", it sets KEY_SCHEME="https" and KEY_HOST="example.com:8899".
func splitEnvURL(envVars map[string]string, key, url string) {
	if idx := strings.Index(url, "://"); idx >= 0 {
		envVars[key+"_SCHEME"] = url[:idx]
		envVars[key+"_HOST"] = url[idx+3:]
	} else {
		envVars[key] = url
	}
}

// validLabelValue reports whether s is a valid Kubernetes label value.
// A valid label value must be 63 characters or less, and must match
// (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])? — i.e. empty or
// alphanumeric at start/end with [-_.a-zA-Z0-9] in between.
func validLabelValue(s string) bool {
	if len(s) > 63 {
		return false
	}
	if s == "" {
		return true
	}
	for i, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		if c == '-' || c == '_' || c == '.' {
			if i == 0 || i == len(s)-1 {
				return false // must start/end with alphanumeric
			}
			continue
		}
		return false
	}
	return true
}

// lastMessagesSnapshot finds the last MESSAGES_SNAPSHOT event in aguiEvents
// and returns its messages. Returns nil if none found.
func lastMessagesSnapshot(events []json.RawMessage) []ambientExportMessage {
	var lastMsgs []ambientExportMessage
	for _, raw := range events {
		var ev aguiEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		if ev.Type == "MESSAGES_SNAPSHOT" && len(ev.Messages) > 0 {
			lastMsgs = ev.Messages
		}
	}
	return lastMsgs
}
