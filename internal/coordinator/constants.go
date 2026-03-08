package coordinator

import "time"

const (
	// EventLogCap is the maximum number of entries retained in Server.EventLog.
	EventLogCap = 200

	// SSEBufCap is the maximum number of buffered SSE events per agent (ring buffer).
	SSEBufCap = 200

	// MaxBodySize is the default maximum request body size for agent/space writes (1 MiB).
	MaxBodySize = 1 << 20

	// MaxReplyBodySize is the maximum body size for boss reply messages (32 KiB).
	MaxReplyBodySize = 32 * 1024

	// MaxDismissBodySize is the maximum body size for dismiss-question requests (4 KiB).
	MaxDismissBodySize = 4 * 1024

	// StalenessThreshold is the duration after which an active agent that has not
	// self-reported is marked stale.
	StalenessThreshold = 15 * time.Minute
)
