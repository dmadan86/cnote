package modules

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// knownEdges are the module-to-module imports that exist today (18 Nov 2026). The owner wants every module to be
// extractable into its own service later, so modules must reach each other through registry commands (c.Call) or
// events, not imports. Shrink this list when you remove a coupling; never add to it without an architecture
// decision recorded in docs/architecture/service-boundaries.md.
var knownEdges = map[string]bool{
	"admin->security":       true,
	"agents->ai":            true,
	"ai->notes":             true,
	"bank->expenses":        true,
	"bank->receipts":        true,
	"billing->inventory":    true,
	"billing->notes":        true,
	"company->billing":      true,
	"company->security":     true,
	"crm->notes":            true,
	"docs->ai":              true,
	"docs->notes":           true,
	"emailtpl->docs":        true,
	"expenses->billing":     true,
	"expenses->notes":       true,
	"home->billing":         true,
	"home->hr":              true,
	"home->inventory":       true,
	"importers->notes":      true,
	"leads->billing":        true,
	"leads->emailtpl":       true,
	"leads->notes":          true,
	"marketplace->admin":    true,
	"payroll->billing":      true,
	"projects->notes":       true,
	"publiclinks->billing":  true,
	"publiclinks->emailtpl": true,
	"purchases->billing":    true,
	"purchases->inventory":  true,
	"purchases->notes":      true,
	"quotes->billing":       true,
	"quotes->notes":         true,
	"receipts->billing":     true,
	"receipts->notes":       true,
	"recurring->billing":    true,
	"recurring->notes":      true,
	"reports->billing":      true,
	"revisions->notes":      true,
	"sharing->notes":        true,
	"timelog->billing":      true,
}

func TestModuleBoundaries(t *testing.T) {
	const prefix = "github.com/cnote/api/internal/modules/"
	dirs, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(d.Name(), "*.go"))
		seen := map[string]bool{}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, im := range af.Imports {
				p := strings.Trim(im.Path.Value, `"`)
				if !strings.HasPrefix(p, prefix) {
					continue
				}
				dep := strings.SplitN(strings.TrimPrefix(p, prefix), "/", 2)[0]
				edge := d.Name() + "->" + dep
				if dep != d.Name() && !knownEdges[edge] && !seen[edge] {
					seen[edge] = true
					added = append(added, edge)
				}
			}
		}
	}
	sort.Strings(added)
	if len(added) > 0 {
		t.Fatalf("new cross-module imports %v: call the other module through a registry command (c.Call) or an event instead, see docs/architecture/service-boundaries.md", added)
	}
}
