package arch_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	modulePrefix = "github.com/daggix02-m/event_nu_backend/internal/"
	servicePath  = modulePrefix + "service"
	domainPath   = modulePrefix + "domain"
)

// This guard pins the hexagonal dependency direction:
//
//	internal/domain  — the core. Depends transitively on no other internal
//	                   package.
//	internal/service — application/business rules. May transitively depend
//	                   only on domain, shared, validator, and config. Must
//	                   never depend — directly or through any dependency —
//	                   on the transport layer (api) or on adapters
//	                   (repository, infrastructure/*).
//
// Dependencies are checked over the full transitive closure (`go list
// -deps`), not just direct imports: a core package that smuggles an adapter
// in through an intermediate package is still coupled to it.
//
// The rule was built up slice-by-slice with an exception ledger; the last
// entry (infrastructure/storage, the media slice) was cleared when that
// slice landed, so the rule now runs at full strength with no exceptions.
//
// Tests are excluded (no -test flag), so test files are free to wire real
// repositories and infrastructure directly — they are part of the
// test-fixture adapter, not the core.

func internalDepsTransitive(t *testing.T, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pattern).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v: %s", pattern, err, out)
	}
	var deps []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if imp, ok := strings.CutPrefix(line, modulePrefix); ok && strings.TrimSpace(imp) != "" {
			deps = append(deps, imp)
		}
	}
	return deps
}

func TestDomainDependsOnlyOnStdlib(t *testing.T) {
	var unexpected []string
	for _, d := range internalDepsTransitive(t, domainPath) {
		if d != "domain" {
			unexpected = append(unexpected, d)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("internal/domain (the core) must transitively depend on no other internal package; found: %v", unexpected)
	}
}

func TestServiceAvoidsTransportAndAdapters(t *testing.T) {
	allowed := []string{"domain", "shared", "validator", "config", "service"}

	var unexpected []string
	for _, d := range internalDepsTransitive(t, servicePath) {
		if !slices.Contains(allowed, d) {
			unexpected = append(unexpected, d)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("internal/service (directly or transitively) depends on transport/adapter packages; the core may only use domain, shared, validator, and config: %v", unexpected)
	}
}
