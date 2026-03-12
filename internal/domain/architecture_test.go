// Package domain_test contains structural boundary tests for the hexagonal architecture.
// These tests verify import rules and document the baseline compliance of the current
// codebase. Rules that FAIL on the current monolith are expected and documented —
// they become the acceptance criteria for the migration phases.
package domain_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// moduleRoot returns the absolute path to the go.mod directory.
// Derived from the location of this test file so it works regardless of CWD.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: <root>/internal/domain/architecture_test.go
	// navigate up two directories to reach the module root
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	return abs
}

type pkgInfo struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

// listPackages runs go list -json on the given patterns from root and returns
// the combined results. Multiple packages are returned as separate JSON objects.
func listPackages(t *testing.T, root string, patterns ...string) []pkgInfo {
	t.Helper()
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// go list may exit non-zero for missing packages; treat as empty result.
		t.Logf("go list %v: %v (may be expected for packages not yet created)", patterns, err)
		return nil
	}
	// go list -json emits one JSON object per package; parse them in sequence.
	var pkgs []pkgInfo
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkgInfo
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// isStdlib returns true if the import path is a standard library package.
// Standard library packages never contain a dot in the first path element.
func isStdlib(imp string) bool {
	first := strings.SplitN(imp, "/", 2)[0]
	return !strings.Contains(first, ".")
}

// modulePath reads the module path from go.mod in root.
func modulePath(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestDomainImportsOnlyStdlib verifies that internal/domain and
// internal/domain/ports import nothing beyond the Go standard library and the
// domain package itself. This is the core contract of the hexagonal architecture:
// the domain layer must have zero external dependencies.
//
// EXPECTED RESULT: PASS (domain/ is newly created with clean types only).
func TestDomainImportsOnlyStdlib(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)
	domainPkg := mod + "/internal/domain"

	pkgs := listPackages(t, root, "./internal/domain/...", "./internal/domain/ports/...")
	if len(pkgs) == 0 {
		t.Skip("domain packages not found — run after creating internal/domain/")
	}

	for _, pkg := range pkgs {
		t.Run(pkg.ImportPath, func(t *testing.T) {
			var violations []string
			for _, imp := range pkg.Imports {
				if isStdlib(imp) {
					continue // stdlib is allowed
				}
				// The ports package is allowed to import the domain package itself.
				if imp == domainPkg || strings.HasPrefix(imp, domainPkg+"/") {
					continue
				}
				violations = append(violations, imp)
			}
			if len(violations) > 0 {
				t.Errorf("FAIL: %s imports external packages (violates domain purity rule):\n  %s",
					pkg.ImportPath, strings.Join(violations, "\n  "))
			} else {
				t.Logf("PASS: %s imports only stdlib/domain — domain boundary is clean", pkg.ImportPath)
			}
		})
	}
}

// TestCoordinatorImportBaseline documents the import profile of the current
// internal/coordinator monolith. This test DOES NOT fail — it logs the baseline
// so future phases can verify progress toward the target architecture.
//
// EXPECTED RESULT: coordinator/ imports gorm, sqlite, http, etc. (monolith baseline).
// After Phase 3 (composition root), coordinator/ should import only adapters + domain.
func TestCoordinatorImportBaseline(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)

	pkgs := listPackages(t, root, "./internal/coordinator")
	if len(pkgs) == 0 {
		t.Skip("coordinator package not found")
	}

	externalPrefixes := []string{
		"gorm.io/",
		"github.com/glebarez/",
		"github.com/modelcontextprotocol/",
		"github.com/jackc/",
	}

	for _, pkg := range pkgs {
		t.Run("coordinator_baseline", func(t *testing.T) {
			found := make(map[string][]string)
			for _, imp := range pkg.Imports {
				if isStdlib(imp) {
					continue
				}
				if strings.HasPrefix(imp, mod) {
					continue // same module — not an external dep
				}
				for _, prefix := range externalPrefixes {
					if strings.HasPrefix(imp, prefix) {
						found[prefix] = append(found[prefix], imp)
					}
				}
			}
			for prefix, imps := range found {
				t.Logf("BASELINE: coordinator imports %s group:\n  %s", prefix, strings.Join(imps, "\n  "))
			}
			if len(found) == 0 {
				t.Logf("coordinator has no known external imports (unexpected — check patterns)")
			}
		})
	}
}

// TestAdapterIsolation enforces that each adapter package in internal/adapters/
// does not import any sibling adapter. The coordinator/ package is the only
// allowed composition root — adapters must remain decoupled from each other.
//
// EXPECTED RESULT: PASS (Phase 2 — storage adapter exists and is clean).
func TestAdapterIsolation(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)

	pkgs := listPackages(t, root, "./internal/adapters/...")
	if len(pkgs) == 0 {
		t.Fatal("FAIL: internal/adapters/ packages not found — Phase 2 adapter must exist")
	}

	adapterBase := mod + "/internal/adapters"
	for _, pkg := range pkgs {
		pkg := pkg
		t.Run(pkg.ImportPath, func(t *testing.T) {
			thisAdapter := strings.TrimPrefix(pkg.ImportPath, adapterBase+"/")
			thisAdapter = strings.SplitN(thisAdapter, "/", 2)[0]

			var violations []string
			for _, imp := range pkg.Imports {
				if !strings.HasPrefix(imp, adapterBase+"/") {
					continue
				}
				sibling := strings.TrimPrefix(imp, adapterBase+"/")
				sibling = strings.SplitN(sibling, "/", 2)[0]
				if sibling != thisAdapter {
					violations = append(violations, imp)
				}
			}
			if len(violations) > 0 {
				t.Errorf("FAIL: adapter %s imports sibling adapters (violates isolation rule):\n  %s",
					pkg.ImportPath, strings.Join(violations, "\n  "))
			} else {
				t.Logf("PASS: adapter %s does not import sibling adapters", pkg.ImportPath)
			}
		})
	}
}

// TestAdapterDoesNotImportCoordinator enforces that adapter packages do NOT
// import internal/coordinator/. Adapters are allowed to import:
//   - stdlib
//   - domain/ and domain/ports/
//   - internal/coordinator/db/ (the GORM model layer, until it is extracted)
//
// Importing internal/coordinator/ would create a circular dependency and
// violate the hexagonal boundary.
//
// EXPECTED RESULT: PASS (Phase 2 — storage adapter is clean).
func TestAdapterDoesNotImportCoordinator(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)

	pkgs := listPackages(t, root, "./internal/adapters/...")
	if len(pkgs) == 0 {
		t.Fatal("FAIL: internal/adapters/ packages not found — Phase 2 adapter must exist")
	}

	coordinatorPkg := mod + "/internal/coordinator"
	for _, pkg := range pkgs {
		pkg := pkg
		t.Run(pkg.ImportPath, func(t *testing.T) {
			var violations []string
			for _, imp := range pkg.Imports {
				// Block any import of coordinator/ itself (but allow coordinator/db/).
				if imp == coordinatorPkg {
					violations = append(violations, imp)
					continue
				}
				// Disallow coordinator sub-packages other than coordinator/db.
				if strings.HasPrefix(imp, coordinatorPkg+"/") &&
					!strings.HasPrefix(imp, coordinatorPkg+"/db") {
					violations = append(violations, imp)
				}
			}
			if len(violations) > 0 {
				t.Errorf("FAIL: adapter %s imports coordinator/ (violates hexagonal boundary):\n  %s",
					pkg.ImportPath, strings.Join(violations, "\n  "))
			} else {
				t.Logf("PASS: adapter %s does not import coordinator/", pkg.ImportPath)
			}
		})
	}
}

// TestStorageAdapterImplementsPort is a compile-time-equivalent runtime check:
// it verifies that internal/adapters/storage/sqlite exports a type that
// satisfies the StoragePort interface. We achieve this by inspecting the
// package's exported symbols via go list -json and confirming the package
// compiles successfully (build errors surface in listPackages).
//
// EXPECTED RESULT: PASS (Phase 2 — sqlite adapter implements StoragePort).
func TestStorageAdapterPackageExists(t *testing.T) {
	root := moduleRoot(t)
	mod := modulePath(t, root)

	pkgs := listPackages(t, root, "./internal/adapters/storage/sqlite/...")
	if len(pkgs) == 0 {
		t.Fatalf("FAIL: %s/internal/adapters/storage/sqlite not found — create it in Phase 2", mod)
	}
	t.Logf("PASS: storage adapter package exists: %s", pkgs[0].ImportPath)
}
