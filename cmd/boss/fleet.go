package main

// fleet.go — "boss export" and "boss import" CLI commands (TASK-104).
//
// Export: GET /spaces/:space/export → write YAML to stdout or file.
// Import: parse fleet file, detect cycles, topo-sort, upsert personas and
// agents via server REST primitives. Supports dry-run with persona prompt diffs.

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ambient/platform/components/boss/internal/coordinator"
)

// cmdExport handles "boss export <space> [--output fleet.yaml]".
func cmdExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	output := fs.String("output", "", "Output file path (default: stdout)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Export a space as an agent-compose.yaml fleet file.

Includes agents, personas, and space metadata. Excludes tasks, tokens,
session IDs, and any runtime-only state. Credentials (e.g. userinfo in
repo_url) are stripped automatically.

Usage:
  boss export <space> [--output fleet.yaml]

Examples:
  boss export my-space
  boss export my-space --output fleet.yaml

Options:
  --output string   File to write (default: stdout)

Environment:
  BOSS_URL         Coordinator URL  (default: http://localhost:8899)
  BOSS_API_TOKEN   Bearer token for authenticated requests (optional)
`)
	}
	fs.Parse(args)

	positional := fs.Args()
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "boss export: space name required")
		fs.Usage()
		os.Exit(1)
	}

	client := newClient(positional[0])
	data, err := client.ExportFleet()
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss export: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		if err := os.WriteFile(*output, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "boss export: write %s: %v\n", *output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "exported %d bytes to %s\n", len(data), *output)
	} else {
		os.Stdout.Write(data) //nolint:errcheck
	}
}

// cmdImport handles "boss import <file> [flags]".
func cmdImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	spaceFlag := fs.String("space", "", "Target space (default: space.name in fleet file)")
	dryRun := fs.Bool("dry-run", false, "Show planned changes without applying")
	yes := fs.Bool("yes", false, "Skip confirmation prompt")
	noCreate := fs.Bool("no-create-space", false, "Fail if the target space does not exist")
	fs.Bool("spawn-after-import", false, "Spawn agents after import (reserved for Phase 2)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Import an agent-compose.yaml fleet file into a space.

Semantics: kubectl-apply style sync — create new agents and personas, update
changed ones, leave unmentioned agents alone. Parents are applied before
children (topological order). Cycles are detected and rejected.

The server validates command and work_dir fields on import. Invalid values
are rejected before any changes are applied.

Usage:
  boss import <file> [flags]

Examples:
  boss import fleet.yaml --dry-run
  boss import fleet.yaml --space my-space --yes
  boss import fleet.yaml --no-create-space

Options:
  --space string          Target space (default: space.name from fleet file)
  --dry-run               Show planned changes without applying
  --yes                   Skip confirmation prompt
  --no-create-space       Fail if the target space does not exist
  --spawn-after-import    Spawn agents after import (reserved)

Environment:
  BOSS_URL         Coordinator URL  (default: http://localhost:8899)
  BOSS_API_TOKEN   Bearer token for authenticated requests (optional)
`)
	}
	fs.Parse(args)

	positional := fs.Args()
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "boss import: fleet file required")
		fs.Usage()
		os.Exit(1)
	}

	data, err := os.ReadFile(positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss import: read %s: %v\n", positional[0], err)
		os.Exit(1)
	}

	// ParseAndValidateFleetFile runs the same server-side validations so errors
	// are surfaced locally before any network calls.
	ff, err := coordinator.ParseAndValidateFleetFile(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss import: %v\n", err)
		os.Exit(1)
	}

	targetSpace := ff.Space.Name
	if *spaceFlag != "" {
		targetSpace = *spaceFlag
	}
	if targetSpace == "" {
		fmt.Fprintln(os.Stderr, "boss import: space name required (--space or space.name in fleet file)")
		os.Exit(1)
	}

	// Detect cycles and compute topo order before touching the server.
	order, err := coordinator.TopoSortAgents(ff.Agents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss import: %v\n", err)
		os.Exit(1)
	}

	client := newClient(targetSpace)

	if *dryRun {
		fleetDryRun(client, ff, targetSpace, order)
		return
	}

	if !*yes {
		fmt.Printf("Import fleet into space %q?\n", targetSpace)
		fmt.Printf("  %d persona(s)  %d agent(s)  order: %s\n",
			len(ff.Personas), len(ff.Agents), strings.Join(order, " → "))
		fmt.Print("Proceed? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("aborted")
			os.Exit(0)
		}
	}

	if err := fleetApply(client, ff, targetSpace, order, *noCreate); err != nil {
		fmt.Fprintf(os.Stderr, "boss import: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("imported %d persona(s) and %d agent(s) into space %q\n",
		len(ff.Personas), len(ff.Agents), targetSpace)
}

// ─── Dry-run ──────────────────────────────────────────────────────────────────

// fleetDryRun prints what fleetApply would do without making any changes.
func fleetDryRun(client *coordinator.Client, ff *coordinator.FleetFile, spaceName string, order []string) {
	fmt.Printf("=== dry-run: import into %q ===\n\n", spaceName)
	anyChange := false

	if len(ff.Personas) > 0 {
		fmt.Println("Personas:")
		for id, fp := range ff.Personas {
			existing, err := client.FetchPersona(id)
			if err != nil {
				fmt.Printf("  [?] %s  (fetch error: %v)\n", id, err)
				continue
			}
			if existing == nil {
				fmt.Printf("  [+] %s  would create\n", id)
				anyChange = true
			} else if existing.Name != fp.Name || existing.Description != fp.Description || existing.Prompt != fp.Prompt {
				fmt.Printf("  [~] %s  would update\n", id)
				if existing.Prompt != fp.Prompt {
					printPromptDiff(existing.Prompt, fp.Prompt)
				}
				anyChange = true
			} else {
				fmt.Printf("  [=] %s  no change\n", id)
			}
		}
		fmt.Println()
	}

	if len(ff.Agents) > 0 {
		fmt.Printf("Agents (apply order: %s):\n", strings.Join(order, " → "))
		for _, name := range order {
			fa := ff.Agents[name]
			cfg, err := client.FetchAgentConfig(name)
			if err != nil {
				fmt.Printf("  [?] %s  (fetch error: %v)\n", name, err)
				continue
			}
			// cfg == nil means the space wasn't found; empty cfg means new agent.
			if cfg == nil || (cfg.Backend == "" && cfg.Command == "" && cfg.WorkDir == "" && cfg.InitialPrompt == "") {
				fmt.Printf("  [+] %s  would create\n", name)
				anyChange = true
			} else {
				diffs := fleetAgentConfigDiff(cfg, fa)
				if len(diffs) == 0 {
					fmt.Printf("  [=] %s  no change\n", name)
				} else {
					fmt.Printf("  [~] %s  would update: %s\n", name, strings.Join(diffs, ", "))
					anyChange = true
				}
			}
		}
		fmt.Println()
	}

	if !anyChange {
		fmt.Println("No changes needed.")
	}
}

// ─── Apply ────────────────────────────────────────────────────────────────────

// fleetApply applies the fleet file to the target space.
func fleetApply(client *coordinator.Client, ff *coordinator.FleetFile, spaceName string, order []string, noCreate bool) error {
	// Step 1: ensure space exists.
	if noCreate {
		exists, err := client.SpaceExists()
		if err != nil {
			return fmt.Errorf("check space: %w", err)
		}
		if !exists {
			return fmt.Errorf("space %q does not exist (--no-create-space set)", spaceName)
		}
	} else {
		if _, err := client.EnsureSpace(); err != nil {
			return fmt.Errorf("ensure space: %w", err)
		}
	}

	// Step 2: upsert personas.
	for id, fp := range ff.Personas {
		existing, err := client.FetchPersona(id)
		if err != nil {
			return fmt.Errorf("fetch persona %q: %w", id, err)
		}
		if existing == nil {
			if _, err := client.CreatePersona(&coordinator.Persona{
				ID:          id,
				Name:        fp.Name,
				Description: fp.Description,
				Prompt:      fp.Prompt,
			}); err != nil {
				return fmt.Errorf("create persona %q: %w", id, err)
			}
			fmt.Printf("  [+] persona %s\n", id)
		} else if existing.Name != fp.Name || existing.Description != fp.Description || existing.Prompt != fp.Prompt {
			if err := client.UpdatePersona(id, fp.Name, fp.Description, fp.Prompt); err != nil {
				return fmt.Errorf("update persona %q: %w", id, err)
			}
			fmt.Printf("  [~] persona %s\n", id)
		}
	}

	// Step 3: upsert agents in topological order (parents before children).
	for _, name := range order {
		fa := ff.Agents[name]

		// Ensure the agent record exists (idempotent: POST sets status only,
		// never overwrites an existing agent's meaningful state).
		update := &coordinator.AgentUpdate{
			Status:  coordinator.StatusIdle,
			Summary: "imported via agent-compose",
			Role:    fa.Role,
			Parent:  fa.Parent,
		}
		if err := client.PostAgentUpdate(name, update); err != nil {
			return fmt.Errorf("create agent %q: %w", name, err)
		}

		// Build the config from the fleet agent entry.
		personaRefs := make([]coordinator.PersonaRef, 0, len(fa.Personas))
		for _, pid := range fa.Personas {
			personaRefs = append(personaRefs, coordinator.PersonaRef{ID: pid})
		}
		cfg := &coordinator.AgentConfig{
			WorkDir:       fa.WorkDir,
			InitialPrompt: fa.InitialPrompt,
			Backend:       fa.Backend,
			Command:       fa.Command,
			RepoURL:       fa.RepoURL,
			Model:         fa.Model,
			Personas:      personaRefs,
		}
		if err := client.PatchAgentConfig(name, cfg); err != nil {
			return fmt.Errorf("patch config for %q: %w", name, err)
		}
		fmt.Printf("  [ok] agent %s\n", name)
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// fleetAgentConfigDiff returns a list of field names that differ between the
// current stored config and the fleet agent definition.
func fleetAgentConfigDiff(cfg *coordinator.AgentConfig, fa coordinator.FleetAgent) []string {
	var diffs []string
	if fa.WorkDir != "" && cfg.WorkDir != fa.WorkDir {
		diffs = append(diffs, "work_dir")
	}
	if fa.Backend != "" && cfg.Backend != fa.Backend {
		diffs = append(diffs, "backend")
	}
	if fa.Command != "" && cfg.Command != fa.Command {
		diffs = append(diffs, "command")
	}
	if fa.InitialPrompt != "" && cfg.InitialPrompt != fa.InitialPrompt {
		diffs = append(diffs, "initial_prompt")
	}
	if fa.RepoURL != "" && cfg.RepoURL != fa.RepoURL {
		diffs = append(diffs, "repo_url")
	}
	if fa.Model != "" && cfg.Model != fa.Model {
		diffs = append(diffs, "model")
	}
	return diffs
}

// printPromptDiff prints a line-by-line diff of two prompt strings.
// Lines present in old but not new are prefixed with "-"; new lines with "+".
// This gives reviewers visibility into prompt changes before they are applied.
func printPromptDiff(oldPrompt, newPrompt string) {
	oldLines := strings.Split(oldPrompt, "\n")
	newLines := strings.Split(newPrompt, "\n")
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	for i := 0; i < maxLen; i++ {
		switch {
		case i < len(oldLines) && i < len(newLines):
			if oldLines[i] != newLines[i] {
				fmt.Printf("      - %s\n", oldLines[i])
				fmt.Printf("      + %s\n", newLines[i])
			}
		case i < len(oldLines):
			fmt.Printf("      - %s\n", oldLines[i])
		default:
			fmt.Printf("      + %s\n", newLines[i])
		}
	}
}
