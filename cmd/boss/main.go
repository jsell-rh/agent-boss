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
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "boss: unknown command %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `boss — multi-agent coordination bus

Commands:
  serve                     Start the coordinator server
  init [space-name]         Create space and register MCP server with Claude
  attach                    Attach to an agent's tmux session
  post                      Post an agent status update
  get                       Get agent state or space markdown
  spaces                    List all spaces
  delete                    Delete a space or agent
  ignite                    Generate ignition prompt for an agent
  broadcast                 Trigger boss.check broadcast for a space

Examples:
  boss serve
  boss init MyProject
  boss init MyProject --open
  boss attach --space my-feature --agent api
  boss post --space my-feature --agent api --status done --summary "shipped"
  boss get --space my-feature --agent api
  boss get --space my-feature --raw
  boss spaces
  boss delete --space my-feature
  boss delete --space my-feature --agent api
  boss ignite SDK sdk-backend-replacement
  boss broadcast --space sdk-backend-replacement

Environment:
  BOSS_URL           Server URL (default: http://localhost:8899)
  BOSS_API_TOKEN     Bearer token for authenticated requests (optional)
  COORDINATOR_PORT   Server port (serve only, default: 8899)
  COORDINATOR_HOST   Hostname used in agent-facing URLs (serve only, default: localhost)
  DATA_DIR           Data directory (serve only, default: ./data)
  FRONTEND_DIR       Vue frontend dist directory (serve only, optional)
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

func cmdBroadcast(args []string) {
	fs := flag.NewFlagSet("broadcast", flag.ExitOnError)
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

func cmdDelete(args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
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
