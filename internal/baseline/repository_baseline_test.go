// Package baseline holds the repository-baseline contract tests
// (frostyard/core ADR-0071, "hold every code repository to one baseline").
//
// One test per baseline row, each reading the live tree through
// `git ls-files`:
//
//	B1  AGENTS.md is canonical; the ACMM aliases are symlinks to it
//	B2  .memory/corrections.jsonl keeps the five-field schema
//	B3  docs/README.md, docs/org-adrs.md and a TEMPLATE.md per category
//	B4  one tool pin (mise.toml), the gate triad, no second linter pin
//	B5  SHA-pinned actions, least-privilege permissions, no persisted
//	    checkout credentials
//	B6  risk paths: the spec, the caller, and a glob matcher
//	B7  the pull request template asks for a risk tier
//	B8  .svu.yml names the tag pattern, and the bump recipe stops on a failure
//	B10 the LICENSE is present and holds the MIT text
//	B12 CI runs `make ci` on pull requests and merge-queue entries
//	B13 Dependabot covers every ecosystem in the tree
//	B14 SECURITY.md names the private reporting channel
//	B15 no CODEOWNERS file
//	B16 no stray agent directories, a minimal .claude/
//	B17 the README is a `tool` README within 1000 words
//
// B9 (docs site) and B11 (Go version) are not checked here: both are follow-up
// changes that this repository does not carry yet.
//
// This package is test-only. It uses the standard library only.
package baseline

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// repoRoot returns the absolute repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %s has no go.mod: %v", root, err)
	}
	return root
}

// trackedFiles lists the tracked files of the repository, in git order.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v", root, err)
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		t.Fatalf("git ls-files in %s returned no file", root)
	}
	return files
}

func trackedSet(t *testing.T) map[string]bool {
	t.Helper()
	set := make(map[string]bool)
	for _, name := range trackedFiles(t) {
		set[name] = true
	}
	return set
}

// readRepoFile reads one repository file.
func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// ---- B1: one canonical instruction file, committed relative symlinks. ----

func TestRepositoryBaselineB1CanonicalAgentFile(t *testing.T) {
	root := repoRoot(t)

	agents := filepath.Join(root, "AGENTS.md")
	info, err := os.Lstat(agents)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("AGENTS.md must be a regular file, got mode %s", info.Mode())
	}

	for _, alias := range []string{
		"CLAUDE.md",
		"GEMINI.md",
		".cursorrules",
		".github/copilot-instructions.md",
	} {
		aliasPath := filepath.Join(root, alias)
		info, err := os.Lstat(aliasPath)
		if err != nil {
			t.Fatalf("lstat %s: %v", alias, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be a symlink to AGENTS.md (ADR-0012), got mode %s", alias, info.Mode())
			continue
		}
		target, err := filepath.EvalSymlinks(aliasPath)
		if err != nil {
			t.Errorf("resolve symlink %s: %v", alias, err)
			continue
		}
		if absTarget, err := filepath.Abs(target); err != nil || absTarget != agents {
			t.Errorf("%s must resolve to %s, got %s", alias, agents, target)
		}
	}
}

// ---- B2: the corrections log keeps its five-field schema. ----

func TestRepositoryBaselineB2CorrectionsSchema(t *testing.T) {
	if !trackedSet(t)[".memory/corrections.jsonl"] {
		t.Fatal(".memory/corrections.jsonl must be tracked")
	}
	text := readRepoFile(t, ".memory/corrections.jsonl")
	want := []string{"date", "scope", "correction", "evidence", "promoted_to"}
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d is not a JSON object: %v", i+1, err)
			continue
		}
		if len(entry) != len(want) {
			t.Errorf("line %d has %d fields, want %d", i+1, len(entry), len(want))
		}
		for _, key := range want {
			if _, ok := entry[key]; !ok {
				t.Errorf("line %d has no %q field", i+1, key)
			}
		}
	}
}

// ---- B3: the four-category docs/ tree. ----

func TestRepositoryBaselineB3DocsTree(t *testing.T) {
	root := repoRoot(t)

	index := readRepoFile(t, "docs/README.md")
	if !strings.Contains(index, "org-adrs.md") {
		t.Error("docs/README.md must index org-adrs.md")
	}
	if _, err := os.Stat(filepath.Join(root, "docs/org-adrs.md")); err != nil {
		t.Errorf("docs/org-adrs.md must exist: %v", err)
	}
	for _, category := range []string{"adr", "design", "specs", "plans"} {
		template := filepath.Join(root, "docs", category, "TEMPLATE.md")
		if _, err := os.Stat(template); err != nil {
			t.Errorf("docs/%s/TEMPLATE.md must exist: %v", category, err)
		}
	}
}

// ---- B4: one tool pin and the gate triad. ----

func TestRepositoryBaselineB4ToolPinsAndGateTriad(t *testing.T) {
	tracked := trackedSet(t)
	for _, name := range []string{"mise.toml", "mise.lock"} {
		if !tracked[name] {
			t.Errorf("%s must be tracked (core ADR-0043)", name)
		}
	}

	mise := readRepoFile(t, "mise.toml")
	for _, pin := range []string{"golangci-lint", "svu"} {
		line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(pin) + ` = "`)
		if !line.MatchString(mise) {
			t.Errorf("mise.toml must pin %s", pin)
		}
	}

	makefile := readRepoFile(t, "Makefile")
	rules := rulePrerequisites(makefile)
	for _, target := range []string{"verify", "check", "ci"} {
		if _, ok := rules[target]; !ok {
			t.Errorf("Makefile has no %s: rule", target)
		}
	}
	if prereqs, ok := rules["ci"]; ok && !contains(prereqs, "verify") {
		t.Errorf("the ci: rule must depend on verify, got %q", prereqs)
	}

	for _, name := range trackedFiles(t) {
		if name == "Makefile" || !strings.HasPrefix(name, ".github/workflows/") {
			continue
		}
		if strings.Contains(readRepoFile(t, name), "golangci-lint@") {
			t.Errorf("%s pins golangci-lint a second time; mise.toml is the only pin", name)
		}
	}
}

// rulePrerequisites maps each Makefile target to its prerequisite words.
func rulePrerequisites(makefile string) map[string][]string {
	rules := make(map[string][]string)
	for _, line := range strings.Split(makefile, "\n") {
		if line == "" || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		target := strings.TrimSpace(line[:colon])
		if strings.ContainsAny(target, " \t") {
			continue
		}
		if _, seen := rules[target]; seen {
			continue
		}
		rules[target] = strings.Fields(line[colon+1:])
	}
	return rules
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// ---- B5: pinned actions, least privilege, no stored credentials. ----

var pinnedActionRef = regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)

func TestRepositoryBaselineB5WorkflowHardening(t *testing.T) {
	workflows := 0
	for _, name := range trackedFiles(t) {
		if !strings.HasPrefix(name, ".github/workflows/") {
			continue
		}
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		workflows++
		for _, issue := range workflowIssues(readRepoFile(t, name)) {
			t.Errorf("%s: %s", name, issue)
		}
	}
	if workflows == 0 {
		t.Fatal("no tracked workflow file")
	}
}

// workflowIssues reports the B5 rule violations in one workflow file.
func workflowIssues(text string) []string {
	var issues []string
	lines := yamlLines(text)

	for _, line := range lines {
		if strings.HasPrefix(line.text, "#") {
			continue
		}
		value, ok := yamlValue(line.text, "uses")
		if !ok {
			continue
		}
		if strings.HasPrefix(value, "./") {
			continue
		}
		if !pinnedActionRef.MatchString(value) {
			issues = append(issues, "uses: must name a full 40-character commit SHA (core ADR-0021), got "+value)
			continue
		}
		if !strings.Contains(line.raw, " # ") {
			issues = append(issues, "the pinned action "+value+" has no version comment")
		}
	}

	// Top-level permissions must be present, empty or read-only.
	permissionsFound := false
	for i, line := range lines {
		if line.indent != 0 {
			continue
		}
		value, ok := yamlValue(line.text, "permissions")
		if !ok {
			continue
		}
		permissionsFound = true
		switch {
		case value == "{}":
			continue
		case value != "":
			if strings.Contains(value, "write") {
				issues = append(issues, "top-level permissions grant "+value)
			}
			continue
		}
		for j := i + 1; j < len(lines) && lines[j].indent > 0; j++ {
			if strings.HasPrefix(lines[j].text, "#") {
				continue
			}
			if strings.Contains(lines[j].text, "write") {
				issues = append(issues, "top-level permissions grant "+lines[j].text)
			}
		}
	}
	if !permissionsFound {
		issues = append(issues, "the workflow has no top-level permissions: key; use permissions: {} or a read-only block")
	}

	issues = append(issues, checkoutStepsWithoutPersistCredentials(text)...)
	return issues
}

// yamlValue returns the value of a `key: value` line, trimmed and without a
// trailing comment.
func yamlValue(text, key string) (string, bool) {
	text = strings.TrimPrefix(text, "- ")
	if !strings.HasPrefix(text, key+":") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(text, key+":"))
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	return value, true
}

type yamlLine struct {
	indent int
	text   string
	raw    string
}

// yamlLines splits text into non-blank lines with their indentation.
func yamlLines(text string) []yamlLine {
	var lines []yamlLine
	for _, raw := range strings.Split(text, "\n") {
		trimmedRight := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(trimmedRight) == "" {
			continue
		}
		indent := len(trimmedRight) - len(strings.TrimLeft(trimmedRight, " "))
		lines = append(lines, yamlLine{indent: indent, text: strings.TrimSpace(trimmedRight), raw: trimmedRight})
	}
	return lines
}

// checkoutStepsWithoutPersistCredentials reports one problem for every step
// that uses actions/checkout and does not set `persist-credentials: false` as
// a direct child of that step's own `with:` block.
//
// The scan is indentation aware: a value below `env:` or another key is not a
// child of `with:`, a commented line is ignored, and any value other than
// false fails.
func checkoutStepsWithoutPersistCredentials(text string) []string {
	lines := yamlLines(text)
	var problems []string

	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i].text, "- ") {
			continue
		}
		stepIndent := lines[i].indent
		end := i + 1
		for end < len(lines) {
			next := lines[end]
			if next.indent < stepIndent || (next.indent == stepIndent && strings.HasPrefix(next.text, "- ")) {
				break
			}
			end++
		}
		block := lines[i:end]

		checkout := false
		keyIndent := stepIndent + 2
		first := true
		for _, line := range block {
			if strings.HasPrefix(line.text, "#") {
				continue
			}
			indent := line.indent
			if strings.HasPrefix(line.text, "- ") {
				indent += 2
			}
			if first || indent < keyIndent {
				keyIndent = indent
				first = false
			}
			if value, ok := yamlValue(line.text, "uses"); ok && strings.HasPrefix(value, "actions/checkout@") {
				checkout = true
			}
		}
		if !checkout {
			continue
		}

		withIndex := -1
		for j, line := range block {
			if line.indent == keyIndent && line.text == "with:" {
				withIndex = j
				break
			}
		}
		if withIndex < 0 {
			problems = append(problems, "an actions/checkout step has no with: block with persist-credentials: false")
			continue
		}

		childIndent := -1
		ok := false
		for j := withIndex + 1; j < len(block); j++ {
			line := block[j]
			if line.indent <= keyIndent {
				break
			}
			if strings.HasPrefix(line.text, "#") {
				continue
			}
			if childIndent < 0 {
				childIndent = line.indent
			}
			if line.indent == childIndent && line.text == "persist-credentials: false" {
				ok = true
			}
		}
		if !ok {
			problems = append(problems, "an actions/checkout step must set persist-credentials: false in its with: block")
		}
	}
	return problems
}

func TestWorkflowIssues(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantProblem int
		wantText    string
	}{
		{
			name: "no top-level permissions key",
			text: `name: CI
on:
  push:
    branches: [main]
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`,
			wantProblem: 1,
			wantText:    "permissions",
		},
		{
			name: "permissions: {}",
			text: `name: CI
on:
  push:
    branches: [main]
permissions: {}
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`,
		},
		{
			name: "permissions: write-all",
			text: `name: CI
on:
  push:
    branches: [main]
permissions: write-all
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`,
			wantProblem: 1,
			wantText:    "permissions",
		},
		{
			name: "a permissions block that writes",
			text: `name: CI
on:
  push:
    branches: [main]
permissions:
  contents: write
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`,
			wantProblem: 1,
			wantText:    "permissions",
		},
		{
			name: "a read-only permissions block",
			text: `name: CI
on:
  push:
    branches: [main]
permissions:
  contents: read
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`,
		},
		{
			name: "an unpinned action",
			text: `name: CI
on:
  push:
    branches: [main]
permissions: {}
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4 # v4.4.0
        with:
          persist-credentials: false
`,
			wantProblem: 1,
			wantText:    "commit SHA",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := workflowIssues(tc.text)
			if len(issues) != tc.wantProblem {
				t.Fatalf("got %d issue(s) %q, want %d", len(issues), issues, tc.wantProblem)
			}
			if tc.wantText == "" {
				return
			}
			if !strings.Contains(issues[0], tc.wantText) {
				t.Errorf("issue %q must mention %q", issues[0], tc.wantText)
			}
		})
	}
}

func TestCheckoutPersistCredentials(t *testing.T) {
	const withSetting = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
`
	const missing = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          fetch-depth: 0
`
	const commentedOut = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          # persist-credentials: false
          fetch-depth: 0
`
	const setToTrue = `jobs:
  ci:
    steps:
      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: true
`
	const underEnv = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          fetch-depth: 0
        env:
          persist-credentials: false
`
	const atStepLevel = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        persist-credentials: false
`
	const twoStepsOnlyFirst = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
      - name: Checkout the other tree
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          fetch-depth: 0
`
	const twoStepsBoth = `jobs:
  ci:
    steps:
      - name: Checkout code
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          persist-credentials: false
      - name: Checkout the other tree
        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0
        with:
          fetch-depth: 0
          persist-credentials: false
`
	const notCheckout = `jobs:
  ci:
    steps:
      - name: Set up Go
        uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0
        with:
          go-version-file: go.mod
`
	cases := []struct {
		name    string
		text    string
		wantBad int
	}{
		{"setting present", withSetting, 0},
		{"setting missing", missing, 1},
		{"commented out", commentedOut, 1},
		{"set to true", setToTrue, 1},
		{"under env", underEnv, 1},
		{"at step level outside with", atStepLevel, 1},
		{"two steps, only the first sets it", twoStepsOnlyFirst, 1},
		{"two steps, both set it", twoStepsBoth, 0},
		{"no checkout step", notCheckout, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := checkoutStepsWithoutPersistCredentials(tc.text)
			if len(problems) != tc.wantBad {
				t.Fatalf("got %d problem(s) %q, want %d", len(problems), problems, tc.wantBad)
			}
		})
	}
}

// ---- B6: risk paths. ----

// riskLevels are the 4 levels of a version 2 risk-path list (core ADR-0078).
var riskLevels = map[string]bool{"L1": true, "L2": true, "L3": true, "L4": true}

var (
	// pinnedPrTriageRef is the value of the caller's `uses:` key (core ADR-0021).
	pinnedPrTriageRef = regexp.MustCompile(`^frostyard/core/\.github/workflows/pr-triage\.yml@[0-9a-f]{40}$`)
	oldCallerRef      = regexp.MustCompile(`uses: frostyard/core/\.github/workflows/risk-paths\.yml@[0-9a-f]{40}`)
	// callsKey matches a `key: value` line whose value opens a block scalar.
	callsBlockScalar = regexp.MustCompile(`^\s*(?:-\s+)?[A-Za-z0-9_.-]+:[ \t]+[|>][-+0-9]*([ \t]+#.*)?$`)
	usesKey          = regexp.MustCompile(`^uses:[ \t]*(.*)$`)
	usesComment      = regexp.MustCompile(`[ \t]+#.*$`)
	trailingComment  = regexp.MustCompile(`[ \t]+#.*$`)
)

type riskPath struct {
	Glob  string `json:"glob"`
	Level string `json:"level"`
	Why   string `json:"why"`
}

type riskPathsFile struct {
	SchemaVersion int        `json:"schema_version"`
	DefaultLevel  string     `json:"default_level"`
	Paths         []riskPath `json:"paths"`
}

func TestRepositoryBaselineB6RiskPaths(t *testing.T) {
	if !trackedSet(t)[".github/risk-paths.json"] {
		t.Fatal(".github/risk-paths.json must be tracked")
	}
	list := readRepoFile(t, ".github/risk-paths.json")
	for _, problem := range riskPathsListProblems(list) {
		t.Errorf(".github/risk-paths.json: %s", problem)
	}
	for _, problem := range riskPathsCallerProblems(readRepoFile(t, ".github/workflows/risk-paths.yml")) {
		t.Errorf(".github/workflows/risk-paths.yml: %s", problem)
	}

	var spec riskPathsFile
	if err := json.Unmarshal([]byte(list), &spec); err != nil {
		t.Fatalf("parse .github/risk-paths.json: %v", err)
	}
	for _, mustMatch := range []string{
		".github/workflows/release.yml",
		".goreleaser.yml",
		".svu.yml",
		"Makefile",
		"mise.toml",
		"mise.lock",
		".github/actions/publish-to-r2/action.yml",
		"scripts/install-release.sh",
		"scripts/upload-to-r2.sh",
		"internal/signer/gpg.go",
		"internal/generator/apk/generator.go",
		"internal/generator/pacman/generator.go",
		"internal/generator/sysext/generator.go",
		"internal/generator/deb/release_signature.go",
	} {
		matched := false
		for _, entry := range spec.Paths {
			if globMatch(entry.Glob, mustMatch) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s matches no risk glob", mustMatch)
		}
	}
}

// riskPathsListProblems validates a version 2 risk-path list against core
// docs/specs/risk-paths.md: schema_version 2, a default_level and a level on
// each entry in L1 to L4, no unknown key, no duplicate glob, and rule C with
// levels. A version 1 list fails.
func riskPathsListProblems(list string) []string {
	var problems []string
	var spec riskPathsFile
	dec := json.NewDecoder(strings.NewReader(list))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return []string{"parse: " + err.Error()}
	}
	if spec.SchemaVersion != 2 {
		problems = append(problems, fmt.Sprintf("schema_version must be 2, got %d", spec.SchemaVersion))
	}
	if !riskLevels[spec.DefaultLevel] {
		problems = append(problems, fmt.Sprintf("default_level must be 1 of L1 to L4, got %q", spec.DefaultLevel))
	}
	if len(spec.Paths) == 0 {
		problems = append(problems, "paths must not be empty")
	}
	seen := make(map[string]bool)
	for _, entry := range spec.Paths {
		if strings.TrimSpace(entry.Glob) == "" {
			problems = append(problems, "every path needs a non-empty glob")
			continue
		}
		if seen[entry.Glob] {
			problems = append(problems, fmt.Sprintf("duplicate glob %q", entry.Glob))
		}
		seen[entry.Glob] = true
		if !riskLevels[entry.Level] {
			problems = append(problems, fmt.Sprintf("glob %q needs a level in L1 to L4, got %q", entry.Glob, entry.Level))
		}
		if strings.TrimSpace(entry.Why) == "" {
			problems = append(problems, fmt.Sprintf("glob %q needs a why", entry.Glob))
		}
		if len(entry.Why) > 200 {
			problems = append(problems, fmt.Sprintf("glob %q has a why of %d characters, the limit is 200", entry.Glob, len(entry.Why)))
		}
		// Rule C: a glob that matches the list or the caller is at L1 or L2.
		for _, required := range []string{".github/risk-paths.json", ".github/workflows/risk-paths.yml"} {
			if globMatch(entry.Glob, required) && entry.Level != "L1" && entry.Level != "L2" {
				problems = append(problems, fmt.Sprintf("glob %q matches %q, so it must be L1 or L2", entry.Glob, required))
			}
		}
	}
	for _, required := range []string{".github/risk-paths.json", ".github/workflows/risk-paths.yml"} {
		if !seen[required] {
			problems = append(problems, fmt.Sprintf("risk-paths.json must list %q", required))
		}
	}
	return problems
}

// yamlStructureLines drops what is not YAML structure: a comment-only line,
// and every line of a block scalar (a line under `key: |` or `key: >`), which
// is script text and never a key. Blank lines are gone already.
func yamlStructureLines(lines []yamlLine) []yamlLine {
	var out []yamlLine
	blockIndent := -1
	for _, line := range lines {
		if strings.HasPrefix(line.text, "#") {
			continue
		}
		if blockIndent >= 0 && line.indent > blockIndent {
			continue
		}
		blockIndent = -1
		// A trailing comment is not part of a key or its value.
		line.text = strings.TrimSpace(trailingComment.ReplaceAllString(line.text, ""))
		out = append(out, line)
		if callsBlockScalar.MatchString(line.raw) {
			blockIndent = line.indent
		}
	}
	return out
}

// topLevelSection returns the lines under the first top-level `key:` of a
// workflow, up to the next top-level key.
func topLevelSection(lines []yamlLine, key string) ([]yamlLine, bool) {
	for i, line := range lines {
		if line.indent != 0 || line.text != key+":" {
			continue
		}
		var section []yamlLine
		for _, child := range lines[i+1:] {
			if child.indent == 0 {
				break
			}
			section = append(section, child)
		}
		return section, true
	}
	return nil, false
}

// childBlock returns the lines under the `name:` key of a section, and whether
// the section holds that key. The keys of a section are its least indented
// lines, so a nested key of another block is not the one found.
func childBlock(section []yamlLine, name string) ([]yamlLine, bool) {
	indent := keyIndent(section)
	for i, line := range section {
		if line.indent != indent || line.text != name+":" {
			continue
		}
		var block []yamlLine
		for _, child := range section[i+1:] {
			if child.indent <= indent {
				break
			}
			block = append(block, child)
		}
		return block, true
	}
	return nil, false
}

// keyIndent is the indent of the keys of a block: its least indented line.
func keyIndent(block []yamlLine) int {
	indent := -1
	for _, line := range block {
		if indent < 0 || line.indent < indent {
			indent = line.indent
		}
	}
	return indent
}

// blockPinsPrTriage reports whether a job calls core's pr-triage workflow at a
// full SHA from its own `uses:` key: a deeper `uses:` is not the job's.
func blockPinsPrTriage(job []yamlLine) bool {
	indent := keyIndent(job)
	for _, line := range job {
		if line.indent != indent {
			continue
		}
		m := usesKey.FindStringSubmatch(line.text)
		if m == nil {
			continue
		}
		value := strings.TrimSpace(usesComment.ReplaceAllString(m[1], ""))
		return pinnedPrTriageRef.MatchString(value)
	}
	return false
}

// blockListsLabeledType reports whether a trigger lists the type labeled.
func blockListsLabeledType(trigger []yamlLine) bool {
	indent := keyIndent(trigger)
	for _, line := range trigger {
		if line.indent != indent || !strings.HasPrefix(line.text, "types:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line.text, "types:"))
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			continue
		}
		for _, name := range strings.Split(strings.Trim(value, "[]"), ",") {
			if strings.TrimSpace(name) == "labeled" {
				return true
			}
		}
	}
	return false
}

// riskPathsCallerProblems checks the pr-triage caller (core ADR-0068, ADR-0077,
// ADR-0078): it runs on pull_request_target, and its job pr-triage, inside the
// top-level `jobs:` mapping, calls core's pr-triage workflow at a full SHA with
// the labeled event under `on: pull_request_target:`. A `uses:` line of another
// job, or the same text inside a `run:` block, is not the caller. A caller that
// still calls the retired risk-paths workflow fails.
func riskPathsCallerProblems(caller string) []string {
	var problems []string
	if oldCallerRef.MatchString(caller) {
		problems = append(problems, "the caller must not call frostyard/core's retired risk-paths workflow")
	}
	lines := yamlStructureLines(yamlLines(caller))
	onSection, _ := topLevelSection(lines, "on")
	trigger, hasTrigger := childBlock(onSection, "pull_request_target")
	if !hasTrigger {
		problems = append(problems, "the caller must run on pull_request_target")
	} else if !blockListsLabeledType(trigger) {
		problems = append(problems, "the caller must run on the labeled event")
	}
	jobsSection, _ := topLevelSection(lines, "jobs")
	job, hasJob := childBlock(jobsSection, "pr-triage")
	if !hasJob {
		problems = append(problems, "the caller must use the job id pr-triage")
	} else if !blockPinsPrTriage(job) {
		problems = append(problems, "the caller must call frostyard/core's pr-triage workflow at a 40-character SHA from the job pr-triage")
	}
	return problems
}

func TestRiskPathsProblems(t *testing.T) {
	const validList = `{"schema_version": 2, "default_level": "L3", "paths": [
		{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance: the list"},
		{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance: the caller"},
		{"glob": "signing/**", "level": "L1", "why": "signing and keys"}
	]}`
	const callerPin = "frostyard/core/.github/workflows/pr-triage.yml@c996ea3fb54bcf1a3c621113522213702a7673e2"
	const validCaller = `name: PR triage
on:
  pull_request_target:
    types: [opened, synchronize, reopened, edited, labeled]
permissions: {}
jobs:
  pr-triage:
    uses: ` + callerPin + ` # pinned
`
	listCases := []struct {
		name    string
		list    string
		wantBad int
	}{
		{"version 2 list", validList, 0},
		{"version 1 list", `{"schema_version": 1, "paths": [
			{"glob": ".github/risk-paths.json", "why": "governance"},
			{"glob": ".github/workflows/risk-paths.yml", "why": "governance"}
		]}`, 6},
		{"missing level", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance"},
			{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance"},
			{"glob": "signing/**", "why": "signing and keys"}
		]}`, 1},
		{"bad default_level", `{"schema_version": 2, "default_level": "L9", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance"},
			{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance"}
		]}`, 1},
		{"unknown key", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance", "owner": "x"}
		]}`, 1},
		{"caller glob at L3", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance"},
			{"glob": ".github/workflows/*.yml", "level": "L3", "why": "workflows"}
		]}`, 2},
		{"caller glob with a question mark at L3", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance: the list"},
			{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance: the caller"},
			{"glob": ".github/workflows/risk-paths.ym?", "level": "L3", "why": "governance: the caller"}
		]}`, 1},
		{"list glob with a leading double star at L3", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance: the list"},
			{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance: the caller"},
			{"glob": "**/.github/risk-paths.json", "level": "L3", "why": "governance: the list"}
		]}`, 1},
		{"list glob with a brace group at L3", `{"schema_version": 2, "default_level": "L3", "paths": [
			{"glob": ".github/risk-paths.json", "level": "L2", "why": "governance: the list"},
			{"glob": ".github/workflows/risk-paths.yml", "level": "L2", "why": "governance: the caller"},
			{"glob": ".github/{risk-paths.jso?,unused}", "level": "L3", "why": "governance: the list"}
		]}`, 1},
	}
	for _, tc := range listCases {
		t.Run(tc.name, func(t *testing.T) {
			problems := riskPathsListProblems(tc.list)
			if len(problems) != tc.wantBad {
				t.Fatalf("got %d problem(s) %q, want %d", len(problems), problems, tc.wantBad)
			}
		})
	}
	callerCases := []struct {
		name    string
		caller  string
		wantBad int
	}{
		{"pr-triage caller", validCaller, 0},
		{"inline comments on the trigger and the job", strings.NewReplacer("on:\n", "on: # events\n", "  pull_request_target:\n", "  pull_request_target: # base branch\n", "labeled]\n", "labeled] # readiness [L1]\n", "  pr-triage:\n", "  pr-triage: # core\n").Replace(validCaller), 0},
		{"caller pins risk-paths.yml", strings.Replace(validCaller, "pr-triage.yml@", "risk-paths.yml@", 1), 2},
		{"caller without the labeled event", strings.Replace(validCaller, ", labeled", "", 1), 1},
		{"caller with another job id", strings.Replace(validCaller, "  pr-triage:", "  risk-paths:", 1), 1},
		{"pinned uses under another job", `name: PR triage
on:
  pull_request_target:
    types: [opened, synchronize, reopened, edited, labeled]
permissions: {}
jobs:
  pr-triage:
    permissions:
      contents: read
    steps:
      - run: echo triage
  triage:
    permissions:
      contents: read
      pull-requests: write
    uses: ` + callerPin + `
`, 1},
		{"uses text in a run block", `name: PR triage
on:
  pull_request_target:
    types: [opened, synchronize, reopened, edited, labeled]
permissions: {}
jobs:
  pr-triage:
    runs-on: ubuntu-latest
    steps:
      - run: |
          uses: ` + callerPin + `
`, 1},
		{"job header inside a run-name block", strings.Replace(validCaller, "  pr-triage:", "  other:", 1) + `run-name: |
  pr-triage:
    uses: ` + callerPin + `
`, 1},
		{"labeled type of another trigger", `name: PR triage
on:
  pull_request:
    types: [labeled]
  pull_request_target:
    types: [opened, synchronize, reopened, edited]
permissions: {}
jobs:
  pr-triage:
    uses: ` + callerPin + `
`, 1},
		{"caller with comments before its uses", `name: PR triage
on:
  pull_request_target:
    types: [opened, synchronize, reopened, edited, labeled]
permissions: {}
jobs:
# a note at indent 0
  pr-triage:
    permissions:
      contents: read
# a note at indent 0 inside the job
  # a note at indent 2 inside the job
    # a note at indent 4, before uses
    uses: ` + callerPin + `
`, 0},
	}
	for _, tc := range callerCases {
		t.Run(tc.name, func(t *testing.T) {
			problems := riskPathsCallerProblems(tc.caller)
			if len(problems) != tc.wantBad {
				t.Fatalf("got %d problem(s) %q, want %d", len(problems), problems, tc.wantBad)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		glob  string
		path  string
		match bool
	}{
		{"scripts/*.sh", "scripts/install-release.sh", true},
		{"scripts/*.sh", "scripts/nested/install-release.sh", false},
		{"internal/signer/**", "internal/signer/gpg.go", true},
		{"internal/signer/**", "internal/signer/nested/deep/gpg.go", true},
		{"internal/signer/**", "internal/other/gpg.go", false},
		{"internal/signer/**", "internal/signer", true},
		// Unicode literals, outside and inside a brace group, and an astral
		// character: `?` is 1 code point, as in core's "u" pattern.
		{"café/**", "café/file.go", true},
		{"{café,thé}/x.go", "thé/x.go", true},
		{"caf?/x", "café/x", true},
		{"a?b", "a😀b", true},
		{"a??b", "a😀b", false},
		{"{😀,x}/*", "😀/f", true},
		{"**/.github/risk-paths.json", ".github/risk-paths.json", true},
		{".github/workflows/risk-paths.ym?", ".github/workflows/risk-paths.yml", true},
		{".github/workflows/risk-paths.ym?", ".github/workflows/risk-paths.yaml", false},
		{"a**b", "a/x/b", false},
		{"a**b", "axxb", true},
		{"**/x", "/x", true},
		{"**", "", false},
		{"{a?,b}", "ax", true},
		{"{a?,b}", "a", false},
		{"{a?,b}", "b", true},
		{".github/{risk-paths.jso?,unused}", ".github/risk-paths.json", true},
		{".github/{risk-paths.jso?,unused}", ".github/unused", true},
		{".github/{risk-paths.jso?,unused}", ".github/other.json", false},
		{"**/index.html", "index.html", true},
		{"**/index.html", "a/b/index.html", true},
		{"**/index.html", "a/b/index.htm", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"{Makefile,mise.toml}", "Makefile", true},
		{"{Makefile,mise.toml}", "mise.toml", true},
		{"{Makefile,mise.toml}", "go.mod", false},
		{"internal/generator/deb/{release,release_signature}.go", "internal/generator/deb/release.go", true},
		{"internal/generator/deb/{release,release_signature}.go", "internal/generator/deb/release_signature.go", true},
		{"internal/generator/deb/{release,release_signature}.go", "internal/generator/deb/release_x.go", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", ".github/actions/ci.yml", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "sub/Makefile", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.glob, tc.path); got != tc.match {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.glob, tc.path, got, tc.match)
		}
	}
}

// globMatch reports whether a risk glob matches a whole repository path, as
// core's matchGlob does (core scripts/lib/risk-paths.mjs): the brace groups are
// expanded first, then each expansion matches whole segments, where `**` as a
// whole segment matches 0 or more segments. An empty path matches nothing.
func globMatch(glob, name string) bool {
	if name == "" {
		return false
	}
	pathSegments := strings.Split(name, "/")
	for _, expansion := range expandBraces(glob) {
		if matchSegments(strings.Split(expansion, "/"), pathSegments) {
			return true
		}
	}
	return false
}

// expandBraces expands the `{a,b}` groups of a glob, as core's expandBraces
// does: left to right, no nesting, at least 2 alternatives, none empty, and at
// most 256 globs. A malformed group gives no expansion, so the glob matches
// nothing.
func expandBraces(glob string) []string {
	const maxBraceExpansions = 256
	var parts [][]string
	literal := ""
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '}':
			return nil
		case '{':
			end := strings.IndexByte(glob[i+1:], '}')
			if end < 0 {
				return nil
			}
			end += i + 1
			if open := strings.IndexByte(glob[i+1:], '{'); open >= 0 && i+1+open < end {
				return nil
			}
			group := strings.Split(glob[i+1:end], ",")
			if len(group) < 2 {
				return nil
			}
			for _, alternative := range group {
				if alternative == "" {
					return nil
				}
			}
			parts = append(parts, []string{literal}, group)
			literal = ""
			i = end
		default:
			// A byte slice keeps a multi-byte UTF-8 character intact.
			literal += glob[i : i+1]
		}
	}
	parts = append(parts, []string{literal})
	expansions := []string{""}
	for _, part := range parts {
		var next []string
		for _, prefix := range expansions {
			for _, alternative := range part {
				next = append(next, prefix+alternative)
			}
		}
		if len(next) > maxBraceExpansions {
			return nil
		}
		expansions = next
	}
	return expansions
}

// matchSegments matches whole segments, as core's matchSegments does: `**`
// matches 0 or more segments, and every other glob segment matches exactly 1
// path segment.
func matchSegments(globSegments, pathSegments []string) bool {
	if len(globSegments) == 0 {
		return len(pathSegments) == 0
	}
	if globSegments[0] == "**" {
		return matchSegments(globSegments[1:], pathSegments) ||
			(len(pathSegments) > 0 && matchSegments(globSegments, pathSegments[1:]))
	}
	if len(pathSegments) == 0 {
		return false
	}
	return segmentMatch(globSegments[0], pathSegments[0]) &&
		matchSegments(globSegments[1:], pathSegments[1:])
}

// segmentMatch matches 1 glob segment, as core's segmentPattern does: `*` is
// any run of characters in the segment and `?` is 1 character of the segment.
func segmentMatch(pattern, segment string) bool {
	var b strings.Builder
	b.WriteString("^")
	// Range over runes, as core's pattern (a "u" regular expression) reads
	// code points: `?` is 1 character, not 1 byte.
	for _, c := range pattern {
		switch c {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(segment)
}

// ---- B7: the pull request template asks for a risk tier. ----

func TestRepositoryBaselineB7RiskTierSection(t *testing.T) {
	template := readRepoFile(t, ".github/pull_request_template.md")
	for _, line := range strings.Split(template, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "Risk tier") {
			return
		}
	}
	t.Fatal(".github/pull_request_template.md needs a heading that contains \"Risk tier\" (core ADR-0019)")
}

// ---- B8: the svu configuration and make bump. ----

func TestRepositoryBaselineB8SvuConfig(t *testing.T) {
	tracked := trackedSet(t)
	if !tracked[".svu.yml"] {
		t.Error(".svu.yml must be tracked (core ADR-0053)")
	}
	if tracked[".svu.yaml"] {
		t.Error(".svu.yaml must not be tracked; the file is .svu.yml")
	}

	svu := readRepoFile(t, ".svu.yml")
	if !strings.Contains(svu, "tag.pattern: \"v0.*\"") {
		t.Error(".svu.yml must set tag.pattern: \"v0.*\", or svu next answers v2.0.0")
	}

	makefile := readRepoFile(t, "Makefile")
	if _, ok := rulePrerequisites(makefile)["bump"]; !ok {
		t.Error("the Makefile needs a bump: rule")
	}
	recipe := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") && strings.Contains(line, "$(SVU) next") {
			recipe = true
			break
		}
	}
	if !recipe {
		t.Error("the bump recipe must call $(SVU) next")
	}
}

// TestRepositoryBaselineB8BumpStopsOnFailure runs the real bump recipe in a
// throwaway git repository: a failed `svu next` or `git tag` must stop it
// before it reports a tag. It never touches the real repository.
func TestRepositoryBaselineB8BumpStopsOnFailure(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := repoRoot(t)
	home := t.TempDir()
	svuPin := svuPinFromMise(t, root)
	fakeSVU := newFakeSVU(t)

	cases := []struct {
		name      string
		next      string
		fail      string
		preTag    string
		wantOK    bool
		wantTag   string
		absentTag string
	}{
		{name: "the tag already exists", next: "v0.0.1", preTag: "v0.0.1"},
		{name: "svu next is off the v0 series", next: "v2.0.0", absentTag: "v2.0.0"},
		{name: "svu next fails", fail: "1"},
		{name: "a free version is tagged", next: "v0.0.1", wantOK: true, wantTag: "v0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, dirEnv := newBumpFixture(t, root, home)
			if tc.preTag != "" {
				mustRun(t, dir, dirEnv, "git", "tag", "-a", tc.preTag, "-m", "pre-existing tag")
			}
			env := append(append([]string{}, dirEnv...),
				"FAKE_SVU_VERSION="+svuPin,
				"FAKE_SVU_NEXT="+tc.next,
				"FAKE_SVU_FAIL="+tc.fail,
			)
			out, err := runMakeBump(t, dir, env, fakeSVU)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("make bump failed: %v\n%s", err, out)
				}
				if !strings.Contains(out, "tagged "+tc.wantTag) {
					t.Errorf("the run must report tagged %s:\n%s", tc.wantTag, out)
				}
				if !tagExists(t, dir, dirEnv, tc.wantTag) {
					t.Errorf("tag %s was not created", tc.wantTag)
				}
				return
			}
			if strings.Contains(out, "tagged") {
				t.Errorf("a failed bump must not report a tag:\n%s", out)
			}
			if err == nil {
				t.Fatalf("make bump must fail here:\n%s", out)
			}
			if tc.absentTag != "" && tagExists(t, dir, dirEnv, tc.absentTag) {
				t.Errorf("tag %s must not exist", tc.absentTag)
			}
		})
	}
}

// TestBumpFixtureIgnoresInheritedGitEnvironment proves a bump fixture cannot be
// redirected: with GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE pointing at a
// decoy repository, the fixture still commits and tags in its own directory,
// and the decoy's configuration, HEAD, tags and index stay byte-identical.
func TestBumpFixtureIgnoresInheritedGitEnvironment(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := repoRoot(t)
	home := t.TempDir()
	svuPin := svuPinFromMise(t, root)

	// The decoy: the repository an inherited environment would reach.
	decoy := t.TempDir()
	decoyEnv := baselineEnv(t, home, filepath.Dir(decoy))
	mustRun(t, decoy, decoyEnv, "git", "init", "-q")
	mustRun(t, decoy, decoyEnv, "git", "config", "core.hooksPath", "/dev/null")
	mustRun(t, decoy, decoyEnv, "git", "config", "user.name", "Repogen Decoy")
	mustRun(t, decoy, decoyEnv, "git", "config", "user.email", "decoy@example.invalid")
	mustRun(t, decoy, decoyEnv, "git", "config", "commit.gpgsign", "false")
	mustRun(t, decoy, decoyEnv, "git", "config", "tag.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(decoy, "decoy.txt"), []byte("decoy\n"), 0o644); err != nil {
		t.Fatalf("write the decoy file: %v", err)
	}
	mustRun(t, decoy, decoyEnv, "git", "add", "decoy.txt")
	mustRun(t, decoy, decoyEnv, "git", "commit", "-q", "-m", "decoy")

	before := decoyState(t, decoy, decoyEnv)

	// Poison this process for the rest of the test.
	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_WORK_TREE", decoy)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoy, ".git", "index"))

	dir, dirEnv := newBumpFixture(t, root, home)
	env := append(append([]string{}, dirEnv...),
		"FAKE_SVU_VERSION="+svuPin,
		"FAKE_SVU_NEXT=v0.0.1",
		"FAKE_SVU_FAIL=",
	)
	out, err := runMakeBump(t, dir, env, newFakeSVU(t))
	if err != nil {
		t.Fatalf("make bump failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "tagged v0.0.1") {
		t.Errorf("the fixture must tag v0.0.1:\n%s", out)
	}
	if !tagExists(t, dir, dirEnv, "v0.0.1") {
		t.Error("the fixture repository holds no v0.0.1 tag")
	}

	after := decoyState(t, decoy, baselineEnv(t, home, filepath.Dir(decoy)))
	if before != after {
		t.Errorf("the decoy repository changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// decoyState summarises a repository: its local configuration, its HEAD, its
// tags and a digest of its index.
func decoyState(t *testing.T, dir string, env []string) string {
	t.Helper()
	config := mustCapture(t, dir, env, "git", "config", "--local", "--list")
	head := mustCapture(t, dir, env, "git", "rev-parse", "HEAD")
	tags := mustCapture(t, dir, env, "git", "tag", "-l")
	index, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatalf("read the decoy index: %v", err)
	}
	return fmt.Sprintf("config:\n%s\nHEAD: %s\ntags:\n%s\nindex: %x", config, head, tags, sha256.Sum256(index))
}

// mustCapture runs one command and returns its trimmed output.
func mustCapture(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newFakeSVU writes an svu stand-in that reports the pinned version and, for
// `next`, the version in FAKE_SVU_NEXT, or fails when FAKE_SVU_FAIL is 1.
func newFakeSVU(t *testing.T) string {
	t.Helper()
	const script = `#!/bin/sh
case "$1" in
--version)
	echo "GitVersion:    $FAKE_SVU_VERSION"
	;;
next)
	if [ "$FAKE_SVU_FAIL" = 1 ]; then
		echo "svu: the tag list is unreadable" >&2
		exit 1
	fi
	echo "$FAKE_SVU_NEXT"
	;;
esac
exit 0
`
	fake := filepath.Join(t.TempDir(), "svu")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write the fake svu: %v", err)
	}
	return fake
}

// baselineEnv returns the environment a bump fixture runs with.
//
// It copies os.Environ() without every GIT_* variable and without MAKEFLAGS,
// MFLAGS and MAKELEVEL. An inherited GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE,
// GIT_OBJECT_DIRECTORY, GIT_COMMON_DIR or GIT_CONFIG* would otherwise send the
// fixture's git commands to another repository. It then adds a private HOME
// with an empty global git configuration, no system configuration, no terminal
// prompt, and a ceiling directory above the fixture, so git cannot walk up out
// of the fixture either.
func baselineEnv(t *testing.T, home, ceiling string) []string {
	t.Helper()

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create the fixture home: %v", err)
	}
	globalConfig := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o644); err != nil {
		t.Fatalf("create the empty global git config: %v", err)
	}

	env := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch {
		case strings.HasPrefix(name, "GIT_"):
			continue
		case name == "MAKEFLAGS", name == "MFLAGS", name == "MAKELEVEL",
			name == "GNUMAKEFLAGS", name == "MAKEFILES":
			// MAKEFILES names extra makefiles that make reads before the
			// fixture's own; they could add a prerequisite to bump.
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+globalConfig,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CEILING_DIRECTORIES="+ceiling,
	)
}

// TestBumpFixtureIgnoresInheritedMakefiles proves that an inherited MAKEFILES
// cannot add work to the fixture's make bump: the injected makefile would
// write a marker outside the fixture, and the marker must not appear.
func TestBumpFixtureIgnoresInheritedMakefiles(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := repoRoot(t)
	home := t.TempDir()
	svuPin := svuPinFromMise(t, root)

	outside := t.TempDir()
	marker := filepath.Join(outside, "marker")
	injected := filepath.Join(outside, "injected.mk")
	rule := "bump: injected-step\ninjected-step:\n\ttouch " + marker + "\n"
	if err := os.WriteFile(injected, []byte(rule), 0o644); err != nil {
		t.Fatalf("write the injected makefile: %v", err)
	}
	t.Setenv("MAKEFILES", injected)

	dir, dirEnv := newBumpFixture(t, root, home)
	env := append(append([]string{}, dirEnv...),
		"FAKE_SVU_VERSION="+svuPin,
		"FAKE_SVU_NEXT=v0.0.1",
		"FAKE_SVU_FAIL=",
	)
	out, err := runMakeBump(t, dir, env, newFakeSVU(t))
	if err != nil {
		t.Fatalf("make bump failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("an inherited MAKEFILES ran a step outside the fixture:\n%s", out)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat the marker: %v", err)
	}
	if !tagExists(t, dir, dirEnv, "v0.0.1") {
		t.Error("the fixture repository holds no v0.0.1 tag")
	}
}

// svuPinFromMise reads the svu version `make svu-version-check` demands.
func svuPinFromMise(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}
	match := regexp.MustCompile(`(?m)^svu = "([^"]+)"`).FindStringSubmatch(string(data))
	if match == nil {
		t.Fatal("mise.toml pins no svu")
	}
	return match[1]
}

// newBumpFixture makes a git repository that holds a copy of the real Makefile
// and mise.toml, committed, with a clean working tree.
func newBumpFixture(t *testing.T, root, home string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	env := baselineEnv(t, home, filepath.Dir(dir))
	for _, name := range []string{"Makefile", "mise.toml"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustRun(t, dir, env, "git", "init", "-q")
	mustRun(t, dir, env, "git", "config", "core.hooksPath", "/dev/null")
	mustRun(t, dir, env, "git", "config", "user.name", "Repogen Baseline Fixture")
	mustRun(t, dir, env, "git", "config", "user.email", "baseline@example.invalid")
	mustRun(t, dir, env, "git", "config", "commit.gpgsign", "false")
	mustRun(t, dir, env, "git", "config", "tag.gpgsign", "false")
	mustRun(t, dir, env, "git", "add", "Makefile", "mise.toml")
	mustRun(t, dir, env, "git", "commit", "-q", "-m", "bump fixture")
	return dir, env
}

// mustRun runs one command and fails the test when it fails.
func mustRun(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// runMakeBump runs `make -C dir bump` against the fake svu.
func runMakeBump(t *testing.T, dir string, env []string, svu string) (string, error) {
	t.Helper()
	cmd := exec.Command("make", "-C", dir, "bump", "SVU="+svu)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tagExists reports whether the fixture repository holds the tag.
func tagExists(t *testing.T, dir string, env []string, tag string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "tag", "-l", tag)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git tag -l %s: %v", tag, err)
	}
	return strings.TrimSpace(string(out)) == tag
}

// ---- B10: the license. ----

func TestRepositoryBaselineB10License(t *testing.T) {
	if !trackedSet(t)["LICENSE"] {
		t.Fatal("LICENSE must be tracked")
	}
	// The file is the MIT text without a title line; GitHub detects it as MIT.
	text := strings.Join(strings.Fields(readRepoFile(t, "LICENSE")), " ")
	for _, clause := range []string{
		"Permission is hereby granted, free of charge, to any person obtaining a copy of this software",
		"WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED",
	} {
		if !strings.Contains(text, clause) {
			t.Errorf("LICENSE must hold the MIT clause %q", clause)
		}
	}
}

// ---- B12: CI runs the one gate. ----

func TestRepositoryBaselineB12CIRunsMakeCI(t *testing.T) {
	if !trackedSet(t)[".github/workflows/ci.yml"] {
		t.Fatal(".github/workflows/ci.yml must be tracked (core ADR-0071 row B12)")
	}
	lines := yamlLines(readRepoFile(t, ".github/workflows/ci.yml"))

	triggers := false
	mergeGroup := false
	for i, line := range lines {
		if line.indent != 0 || line.text != "on:" {
			continue
		}
		for j := i + 1; j < len(lines) && lines[j].indent > 0; j++ {
			if strings.HasPrefix(lines[j].text, "pull_request:") {
				triggers = true
			}
			if strings.HasPrefix(lines[j].text, "merge_group:") {
				mergeGroup = true
			}
		}
	}
	if !triggers {
		t.Error("ci.yml must run on pull_request")
	}
	// The merge queue only reports the required `make ci` check when the
	// workflow also runs on merge_group (core issue #222, rule N3).
	if !mergeGroup {
		t.Error("ci.yml must run on merge_group")
	}

	gate := false
	for _, line := range lines {
		if strings.HasPrefix(line.text, "#") {
			continue
		}
		if line.text == "run: make ci" {
			gate = true
			break
		}
	}
	if !gate {
		t.Error("ci.yml must run `make ci`")
	}
}

// ---- B13: Dependabot covers every ecosystem in the tree. ----

func TestRepositoryBaselineB13DependabotCoverage(t *testing.T) {
	cases := []struct {
		name    string
		tracked []string
		config  string
		missing []string
	}{
		{
			name:    "go module and actions",
			tracked: []string{"go.mod", "main.go"},
			config:  "package-ecosystem: \"gomod\"\npackage-ecosystem: \"github-actions\"\n",
			missing: nil,
		},
		{
			name:    "actions is always required",
			tracked: []string{"README.md"},
			config:  "package-ecosystem: \"gomod\"\n",
			missing: []string{"github-actions"},
		},
		{
			name:    "npm without a config entry",
			tracked: []string{"package.json"},
			config:  "package-ecosystem: \"github-actions\"\n",
			missing: []string{"npm"},
		},
		{
			name:    "cargo, docker and actions all missing",
			tracked: []string{"Cargo.toml", "build/Dockerfile", "ops/Containerfile"},
			config:  "package-ecosystem: \"gomod\"\n",
			missing: []string{"cargo", "docker", "github-actions"},
		},
		{
			name:    "gomod missing",
			tracked: []string{"go.mod"},
			config:  "package-ecosystem: \"npm\"\n",
			missing: []string{"github-actions", "gomod"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := missingEcosystems(tc.tracked, tc.config)
			if strings.Join(got, ",") != strings.Join(tc.missing, ",") {
				t.Fatalf("missingEcosystems = %v, want %v", got, tc.missing)
			}
		})
	}

	if missing := missingEcosystems(trackedFiles(t), readRepoFile(t, ".github/dependabot.yml")); len(missing) != 0 {
		t.Fatalf("Dependabot does not cover %v", missing)
	}
}

// missingEcosystems returns the ecosystems the tree needs and the Dependabot
// configuration does not name, in alphabetical order.
func missingEcosystems(trackedPaths []string, dependabotYAML string) []string {
	required := make(map[string]bool)
	for _, name := range trackedPaths {
		switch path.Base(name) {
		case "go.mod":
			required["gomod"] = true
		case "package.json":
			required["npm"] = true
		case "Cargo.toml":
			required["cargo"] = true
		case "Dockerfile", "Containerfile":
			required["docker"] = true
		}
	}
	required["github-actions"] = true

	var missing []string
	for ecosystem := range required {
		if !strings.Contains(dependabotYAML, `"`+ecosystem+`"`) {
			missing = append(missing, ecosystem)
		}
	}
	sort.Strings(missing)
	return missing
}

// ---- B14: the security policy. ----

func TestRepositoryBaselineB14SecurityPolicy(t *testing.T) {
	tracked := trackedSet(t)
	name := ""
	for _, candidate := range []string{"SECURITY.md", ".github/SECURITY.md"} {
		if tracked[candidate] {
			name = candidate
			break
		}
	}
	if name == "" {
		t.Fatal("SECURITY.md must be tracked (core ADR-0040)")
	}
	if !strings.Contains(readRepoFile(t, name), "https://github.com/frostyard/repogen/security") {
		t.Errorf("%s must name the private reporting URL", name)
	}
}

// ---- B15: no CODEOWNERS file. ----

func TestRepositoryBaselineB15NoCodeowners(t *testing.T) {
	tracked := trackedSet(t)
	for _, name := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if tracked[name] {
			t.Errorf("%s must not be tracked: ownership lives in the roster, not in the repository", name)
		}
	}
}

// ---- B16: no stray agent directories. ----

func TestRepositoryBaselineB16NoStrayAgentTrees(t *testing.T) {
	forbidden := []string{"docs/superpowers/", ".planning/", ".cursor/rules/", ".gaai/", "yeti/", "cairn/"}
	allowedClaude := map[string]bool{
		".claude/settings.json":      true,
		".claude/skills":             true,
		".claude/session-summary.md": true,
	}
	for _, name := range trackedFiles(t) {
		for _, prefix := range forbidden {
			if strings.HasPrefix(name, prefix) {
				t.Errorf("%s is in a retired agent tree; its content belongs in docs/ or .memory/", name)
			}
		}
		if strings.HasPrefix(name, ".claude/") && !allowedClaude[name] {
			t.Errorf("%s is not one of the three .claude/ paths the baseline allows", name)
		}
	}
}

// ---- B17: the README standard. ----

func TestRepositoryBaselineB17Readme(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, "<!-- readme-kind: tool -->") {
		t.Error("README.md needs the line <!-- readme-kind: tool --> (core ADR-0072)")
	}
	if words := len(strings.Fields(readme)); words > 1000 {
		t.Errorf("README.md has %d words, the limit is 1000", words)
	}
	for _, drawing := range []string{"├──", "└──"} {
		if strings.Contains(readme, drawing) {
			t.Errorf("README.md must not hold a tree drawing (%q)", drawing)
		}
	}
}
