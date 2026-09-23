package workflow

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// consultflowRoot is the repository root, relative to this package.
const consultflowRoot = "../../.."

// vistaRoot is a VistA-M checkout containing Packages/. Set VISTA_ROOT to
// verify the legacy citations; the default is the parent directory, which is
// where ConsultFlow was developed inside the VistA-M tree.
func vistaRoot() string {
	if root := os.Getenv("VISTA_ROOT"); root != "" {
		return root
	}
	return "../../../.."
}

// Every rule id cited anywhere in the workflow code must exist.
func TestCitedRulesExist(t *testing.T) {
	ids := map[string]bool{}
	for _, r := range rules {
		if ids[r.ID] {
			t.Errorf("duplicate rule id %s", r.ID)
		}
		ids[r.ID] = true
	}
	for _, d := range actionDefs {
		for _, id := range d.Rules {
			if !ids[id] {
				t.Errorf("action %s cites unknown rule %s", d.Action, id)
			}
		}
	}
	cited := regexp.MustCompile(`"(R-[A-Z0-9-]+)"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "trace.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range cited.FindAllStringSubmatch(string(src), -1) {
			if !ids[m[1]] {
				t.Errorf("%s cites unknown rule %s", f, m[1])
			}
		}
	}
}

// Every legacy reference must point at a real line containing its snippet.
// Skipped when the VistA tree is not present (e.g. inside the Docker build).
func TestLegacyReferencesMatchSource(t *testing.T) {
	root := vistaRoot()
	if _, err := os.Stat(filepath.Join(root, "Packages")); err != nil {
		t.Skip("VistA source tree not available; set VISTA_ROOT to a VistA-M checkout to verify legacy citations")
	}
	for _, r := range rules {
		if len(r.Legacy) == 0 && r.Kind == KindLegacy {
			t.Errorf("%s is LEGACY but cites no source", r.ID)
		}
		for _, ref := range r.Legacy {
			line, err := readLine(filepath.Join(root, ref.File), ref.Line)
			if err != nil {
				t.Errorf("%s: %v", r.ID, err)
				continue
			}
			if strings.TrimSpace(line) != ref.Snippet {
				t.Errorf("%s: %s:%d is not the cited line %q\n  line: %s", r.ID, ref.File, ref.Line, ref.Snippet, line)
			}
		}
	}
}

// Every modern location "path: symbol" must name an existing file.
func TestModernLocationsExist(t *testing.T) {
	for _, r := range rules {
		for _, m := range r.Modern {
			path, _, _ := strings.Cut(m, ":")
			if !strings.HasPrefix(path, "backend/") && !strings.HasPrefix(path, "frontend/") {
				continue // e.g. an HTTP route
			}
			if _, err := os.Stat(filepath.Join(consultflowRoot, path)); err != nil {
				t.Errorf("%s: modern location %s does not exist", r.ID, path)
			}
		}
	}
}

func readLine(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for i := 1; sc.Scan(); i++ {
		if i == n {
			return sc.Text(), nil
		}
	}
	return "", os.ErrNotExist
}
