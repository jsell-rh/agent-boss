package coordinator

import "embed"

// embeddedFrontend holds the compiled Vue SPA assets from frontend/dist.
// The frontend/ directory is populated by running `npm run build` inside
// frontend/ with outDir set to ../internal/coordinator/frontend.
// If the directory only contains .gitkeep (not yet built), the server
// falls back to the legacy mission-control.html dashboard.
//
//go:embed all:frontend
var embeddedFrontend embed.FS
