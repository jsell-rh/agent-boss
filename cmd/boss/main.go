package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ambient/platform/components/boss/internal/coordinator"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "post":
		cmdPost(os.Args[2:])
	case "get":
		cmdGet(os.Args[2:])
	case "spaces":
		cmdSpaces(os.Args[2:])
	case "delete":
		cmdDelete(os.Args[2:])
	case "ignite":
		cmdIgnite(os.Args[2:])
	case "broadcast":
		cmdBroadcast(os.Args[2:])
	case "attach":
		cmdAttach(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "export":
		cmdExport(os.Args[2:])
	case "import":
		cmdImport(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "boss: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `boss — multi-agent coordination bus

Usage:
  boss <command> [flags]

Server Commands:
  serve         Start the coordinator HTTP server
  init          Create a space and register the MCP server with Claude

Client Commands:
  post          Post an agent status update to a space
  get           Get agent state or full space snapshot
  spaces        List all spaces
  attach        Attach to an agent's tmux session
  delete        Delete a space or a single agent from a space
  ignite        Print the ignition prompt for a new agent
  broadcast     Send a boss.check broadcast to all agents in a space
  export        Export a space as an agent-compose.yaml fleet file
  import        Import an agent-compose.yaml fleet file into a space

Use "boss <command> --help" for more information about a command.

Environment (client commands):
  BOSS_URL         Coordinator URL  (default: http://localhost:8899)
  BOSS_API_TOKEN   Bearer token for authenticated requests (optional)
`)
}

func serverURL() string {
	if u := os.Getenv("BOSS_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	port := "8899"
	if p := os.Getenv("COORDINATOR_PORT"); p != "" {
		port = strings.TrimPrefix(p, ":")
	}
	return "http://localhost:" + port
}

// newClient returns a coordinator client configured with the server URL and
// BOSS_API_TOKEN (if set) for authenticated requests.
func newClient(space string) *coordinator.Client {
	c := coordinator.NewClient(serverURL(), space)
	if token := os.Getenv("BOSS_API_TOKEN"); token != "" {
		c.WithAuthToken(token)
	}
	return c
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Start the coordinator HTTP server.

Persists state to SQLite (or Postgres) and serves the agent dashboard and
MCP endpoint. Reads all configuration from environment variables.

Usage:
  boss serve

Examples:
  # Start with default settings (data stored in ./data/, port 8899)
  boss serve

  # Custom data directory and port
  DATA_DIR=/var/lib/boss COORDINATOR_PORT=9000 boss serve

  # Postgres backend
  DB_TYPE=postgres DB_DSN="host=db user=boss dbname=boss sslmode=disable" boss serve

  # Development: serve frontend from disk instead of embedded
  FRONTEND_DIR=./internal/coordinator/frontend boss serve

Environment:
  DATA_DIR              Data directory for SQLite and assets  (default: ./data)
  COORDINATOR_PORT      Listen port                           (default: 8899)
  COORDINATOR_HOST      Hostname for agent-facing URLs        (default: localhost)
  COORDINATOR_EXTERNAL_URL  External URL injected into ambient sessions as BOSS_URL
  DB_TYPE               Database backend: sqlite | postgres   (default: sqlite)
  DB_DSN                Postgres DSN (required when DB_TYPE=postgres)
  FRONTEND_DIR          Override embedded Vue dist directory  (optional)
  BOSS_API_TOKEN        Bearer token for mutating endpoints   (optional — disables auth when unset)
  BOSS_ALLOWED_ORIGINS  Extra CORS origins (comma-separated)  (optional)
  BOSS_ALLOW_SKIP_PERMISSIONS  Pass --dangerously-skip-permissions to Claude (default: false)
  STALENESS_THRESHOLD   Heartbeat staleness cutoff            (default: 5m)
  LOG_FORMAT            Log format: text | json               (default: text)
  AMBIENT_API_URL       Enable ambient session backend; set to API base URL
  AMBIENT_TOKEN         Auth token for ambient API
  AMBIENT_PROJECT       Project identifier for ambient sessions
`)
	}
	fs.Parse(args)

	dataDir, _ := os.Getwd()
	dataDir = filepath.Join(dataDir, "data")
	if envDir := os.Getenv("DATA_DIR"); envDir != "" {
		dataDir = envDir
	}
	dataDir, _ = filepath.Abs(dataDir)

	port := coordinator.DefaultPort
	if envPort := os.Getenv("COORDINATOR_PORT"); envPort != "" {
		if envPort[0] != ':' {
			envPort = ":" + envPort
		}
		port = envPort
	}

	srv := coordinator.NewServer(port, dataDir)

	if frontendDir := os.Getenv("FRONTEND_DIR"); frontendDir != "" {
		absDir, _ := filepath.Abs(frontendDir)
		srv.SetFrontendDir(absDir)
		fmt.Printf("boss: serving Vue frontend from %s\n", absDir)
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("boss: failed to start coordinator: %v", err)
	}
	fmt.Printf("boss: coordinator running on %s (data: %s)\n", port, dataDir)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nboss: shutting down...")
	if err := srv.Stop(); err != nil {
		log.Printf("boss: shutdown error: %v", err)
	}
}

func cmdPost(args []string) {
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Post a status update from an agent to its coordination space.

Agents call this command to report their current status, phase, and next
steps to the boss. Required for the blackboard pattern to work.

Usage:
  boss post --agent <name> --summary <text> [flags]

Examples:
  # Report progress (active, default status)
  boss post --space my-feature --agent api --summary "wiring DB layer" --phase "implementation"

  # Mark work complete
  boss post --agent api --status done --summary "shipped auth endpoint"

  # Signal a blocker
  boss post --agent frontend --status blocked --summary "waiting on API spec" \
      --next "unblock once /spec endpoint is merged"

Options:
  --space string    Space name                                         (default: "default")
  --agent string    Agent name (required)
  --status string   Agent status: active | done | blocked | idle | error  (default: "active")
  --summary string  One-line summary of current work (required)
  --phase string    Current work phase
  --next string     Planned next steps
`)
	}
	space := fs.String("space", "default", "Space name")
	agent := fs.String("agent", "", "Agent name (required)")
	status := fs.String("status", "active", "Status: active|done|blocked|idle|error")
	summary := fs.String("summary", "", "Summary line (required)")
	phase := fs.String("phase", "", "Current phase")
	nextSteps := fs.String("next", "", "Next steps")
	fs.Parse(args)

	if *agent == "" || *summary == "" {
		fmt.Fprintln(os.Stderr, "boss post: --agent and --summary are required")
		fs.Usage()
		os.Exit(1)
	}

	client := newClient(*space)
	update := &coordinator.AgentUpdate{
		Status:    coordinator.AgentStatus(*status),
		Summary:   *summary,
		Phase:     *phase,
		NextSteps: *nextSteps,
	}
	if err := client.PostAgentUpdate(*agent, update); err != nil {
		fmt.Fprintf(os.Stderr, "boss post: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("posted to [%s/%s]: %s\n", *space, *agent, *summary)
}

func cmdGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Get the current state of an agent or an entire space.

Without --agent, returns the full space snapshot as JSON.
With --raw, returns the space as rendered Markdown instead of JSON.

Usage:
  boss get [--agent <name>] [--raw] [flags]

Examples:
  # Full space snapshot (JSON)
  boss get --space my-feature

  # Single agent state (JSON)
  boss get --space my-feature --agent api

  # Rendered Markdown for the whole space
  boss get --space my-feature --raw

Options:
  --space string   Space name           (default: "default")
  --agent string   Agent name (omit for full space snapshot)
  --raw            Return rendered Markdown instead of JSON
`)
	}
	space := fs.String("space", "default", "Space name")
	agent := fs.String("agent", "", "Agent name (omit for full space)")
	raw := fs.Bool("raw", false, "Get rendered markdown")
	fs.Parse(args)

	client := newClient(*space)

	if *raw {
		md, err := client.FetchMarkdown()
		if err != nil {
			fmt.Fprintf(os.Stderr, "boss get: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(md)
		return
	}

	if *agent != "" {
		a, err := client.FetchAgent(*agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "boss get: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(a, "", "  ")
		fmt.Println(string(data))
		return
	}

	ks, err := client.FetchSpace()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss get: %v\n", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(ks, "", "  ")
	fmt.Println(string(data))
}

func cmdSpaces(args []string) {
	fs := flag.NewFlagSet("spaces", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `List all coordination spaces and their agent counts.

Usage:
  boss spaces

Examples:
  boss spaces
`)
	}
	fs.Parse(args)

	client := newClient("")
	spaces, err := client.ListSpaces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss spaces: %v\n", err)
		os.Exit(1)
	}
	if len(spaces) == 0 {
		fmt.Println("no spaces")
		return
	}
	for _, s := range spaces {
		fmt.Printf("  %-24s %d agents   updated %s\n", s.Name, s.AgentCount, s.UpdatedAt.Local().Format("15:04:05"))
	}
}

func cmdIgnite(args []string) {
	fs := flag.NewFlagSet("ignite", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Fetch and print the ignition prompt for a new agent.

The ignition prompt is the startup instructions the agent receives to
connect it to the coordination bus. Redirect the output into a file or
pipe it directly to Claude.

Usage:
  boss ignite [--tmux <session>] <agent-name> <space-name>

Examples:
  # Print ignition prompt for agent "sdk" in space "sdk-backend-replacement"
  boss ignite sdk sdk-backend-replacement

  # Register a specific tmux session alongside the ignition prompt
  boss ignite --tmux my-session sdk sdk-backend-replacement

Options:
  --tmux string   Tmux session name to register (default: auto-detected from $TMUX)
`)
	}
	tmuxSession := fs.String("tmux", "", "Tmux session name to register (default: auto-detect)")
	fs.Parse(args)

	positional := fs.Args()
	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "boss ignite: requires <agent-name> <workspace>")
		fmt.Fprintln(os.Stderr, "usage: boss ignite [-tmux SESSION] SDK sdk-backend-replacement")
		os.Exit(1)
	}
	agentName := positional[0]
	workspace := positional[1]

	client := newClient(workspace)
	prompt, err := client.FetchIgnition(agentName, *tmuxSession)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss ignite: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(prompt)
}

func cmdBroadcast(args []string) {
	fs := flag.NewFlagSet("broadcast", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Send a boss.check broadcast to all agents in a space.

The broadcast signals every registered agent to check in, report status,
and review the shared task board.

Usage:
  boss broadcast [--space <name>]

Examples:
  boss broadcast --space sdk-backend-replacement

Options:
  --space string   Space name  (default: "default")
`)
	}
	space := fs.String("space", "default", "Space name")
	fs.Parse(args)

	client := newClient(*space)
	msg, err := client.TriggerBroadcast()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss broadcast: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(msg)
}

func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Attach to an agent's tmux session.

Looks up the agent's registered session ID and replaces the current process
with tmux attach, handing the terminal directly to the running Claude pane.

Usage:
  boss attach --agent <name> [--space <name>]

Examples:
  boss attach --space my-feature --agent api

Options:
  --space string   Space name           (default: "default")
  --agent string   Agent name (required)
`)
	}
	space := fs.String("space", "default", "Space name")
	agent := fs.String("agent", "", "Agent name (required)")
	fs.Parse(args)

	if *agent == "" {
		fmt.Fprintln(os.Stderr, "boss attach: --agent is required")
		fs.Usage()
		os.Exit(1)
	}

	client := newClient(*space)
	a, err := client.FetchAgent(*agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss attach: %v\n", err)
		os.Exit(1)
	}
	if a.SessionID == "" {
		fmt.Fprintf(os.Stderr, "boss attach: agent %q has no tmux session\n", *agent)
		os.Exit(1)
	}

	tmuxBin, err := lookPath("tmux")
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss attach: tmux not found in PATH\n")
		os.Exit(1)
	}

	// Replace the current process with tmux attach so the terminal is handed
	// over cleanly — no wrapper process sitting between the user and the pane.
	if err := syscall.Exec(tmuxBin, []string{"tmux", "attach", "-t", a.SessionID}, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "boss attach: exec tmux: %v\n", err)
		os.Exit(1)
	}
}

func cmdDelete(args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Delete a space or a single agent from a space.

Usage:
  boss delete --space <name> [--agent <name>]

Examples:
  # Delete a single agent
  boss delete --space my-feature --agent api

  # Delete an entire space and all its agents
  boss delete --space my-feature

Options:
  --space string   Space name (required)
  --agent string   Agent name (omit to delete the entire space)
`)
	}
	space := fs.String("space", "", "Space name (required)")
	agent := fs.String("agent", "", "Agent name (omit to delete entire space)")
	fs.Parse(args)

	if *space == "" {
		fmt.Fprintln(os.Stderr, "boss delete: --space is required")
		fs.Usage()
		os.Exit(1)
	}

	client := newClient(*space)

	if *agent != "" {
		if err := client.DeleteAgent(*agent); err != nil {
			fmt.Fprintf(os.Stderr, "boss delete: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("deleted agent [%s] from space %q\n", *agent, *space)
		return
	}

	if err := client.DeleteSpace(); err != nil {
		fmt.Fprintf(os.Stderr, "boss delete: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deleted space %q\n", *space)
}

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Create a space and register the boss MCP server with the local Claude installation.

Idempotent: safe to run multiple times. Re-registration replaces any existing
boss-mcp entry so the URL is always current.

Usage:
  boss init [space-name] [--open]

Examples:
  # Create (or reconnect to) the default space
  boss init

  # Create a named space and open the dashboard
  boss init my-feature --open

Options:
  --open   Open the space dashboard in your default browser after init
`)
	}
	openBrowser := fs.Bool("open", false, "Open the space URL in your browser")
	fs.Parse(args)

	positional := fs.Args()
	spaceName := "default"
	if len(positional) > 0 {
		spaceName = positional[0]
	}

	baseURL := serverURL()
	client := newClient(spaceName)

	// Step 1: create space if it doesn't exist.
	created, err := client.EnsureSpace()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss init: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("Space %q created.\n", spaceName)
	} else {
		fmt.Printf("Space %q already exists.\n", spaceName)
	}

	// Step 2: register the MCP server with Claude.
	// Remove any existing boss-mcp entry first so re-registration always succeeds.
	mcpURL := baseURL + "/mcp"
	runMCPRegister([]string{"claude", "mcp", "remove", "boss-mcp"}) //nolint:errcheck — ignore if not present
	if err := runMCPRegister([]string{"claude", "mcp", "add", "boss-mcp", "--transport", "http", mcpURL}); err != nil {
		fmt.Fprintf(os.Stderr, "boss init: MCP registration failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "  Run manually: claude mcp add boss-mcp --transport http %s\n", mcpURL)
	} else {
		fmt.Printf("MCP server registered: boss-mcp → %s\n", mcpURL)
	}

	// Step 3: print the dashboard URL.
	dashURL := baseURL + "/spaces/" + spaceName + "/"
	fmt.Printf("Open %s to manage your agents.\n", dashURL)

	// Step 4: optionally open in browser.
	if *openBrowser {
		if err := openURL(dashURL); err != nil {
			fmt.Fprintf(os.Stderr, "boss init: could not open browser: %v\n", err)
		}
	}
}

func runMCPRegister(args []string) error {
	// os/exec is only imported here; avoid importing at top level if not needed.
	// We use the shell to exec since exec.Command is not in scope without importing os/exec.
	// Instead, exec via os.StartProcess for stdlib-only compliance.
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}
	// Find the binary using PATH lookup via os.StartProcess.
	// os.StartProcess requires an absolute path, so use the PATH lookup trick.
	pa := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	// Resolve the binary path manually using PATH.
	bin, err := lookPath(args[0])
	if err != nil {
		return fmt.Errorf("command %q not found: %w", args[0], err)
	}
	proc, err := os.StartProcess(bin, args, pa)
	if err != nil {
		return err
	}
	state, err := proc.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return fmt.Errorf("exited with %s", state)
	}
	return nil
}

// lookPath looks up an executable in PATH directories (stdlib-only alternative to exec.LookPath).
func lookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q: executable file not found in $PATH", name)
}

// openURL opens a URL in the default system browser.
func openURL(url string) error {
	// Try common browser launchers.
	for _, launcher := range []string{"xdg-open", "open", "start"} {
		bin, err := lookPath(launcher)
		if err != nil {
			continue
		}
		proc, err := os.StartProcess(bin, []string{launcher, url}, &os.ProcAttr{
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		})
		if err != nil {
			continue
		}
		proc.Wait() //nolint:errcheck
		return nil
	}
	return fmt.Errorf("no browser launcher found (xdg-open/open/start)")
}
