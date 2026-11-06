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
//	B8  .svu.yml names the tag pattern and `make bump` reads it
//	B10 the LICENSE is present and holds the MIT text
//	B12 CI runs `make ci` on pull requests
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
	"encoding/json"
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
		if !strings.HasSuffix(name, ".yml") || !strings.HasPrefix(name, ".github/workflows/") {
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

	// Top-level permissions must be empty or read-only.
	for i, line := range lines {
		if line.indent != 0 {
			continue
		}
		value, ok := yamlValue(line.text, "permissions")
		if !ok {
			continue
		}
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

type riskPath struct {
	Glob string `json:"glob"`
	Why  string `json:"why"`
}

type riskPathsFile struct {
	SchemaVersion int        `json:"schema_version"`
	Paths         []riskPath `json:"paths"`
}

func TestRepositoryBaselineB6RiskPaths(t *testing.T) {
	if !trackedSet(t)[".github/risk-paths.json"] {
		t.Fatal(".github/risk-paths.json must be tracked")
	}
	var spec riskPathsFile
	if err := json.Unmarshal([]byte(readRepoFile(t, ".github/risk-paths.json")), &spec); err != nil {
		t.Fatalf("parse .github/risk-paths.json: %v", err)
	}
	if spec.SchemaVersion != 1 {
		t.Errorf("schema_version must be 1, got %d", spec.SchemaVersion)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("paths must not be empty")
	}
	seen := make(map[string]bool)
	for _, entry := range spec.Paths {
		if strings.TrimSpace(entry.Glob) == "" {
			t.Error("every path needs a non-empty glob")
			continue
		}
		if seen[entry.Glob] {
			t.Errorf("duplicate glob %q", entry.Glob)
		}
		seen[entry.Glob] = true
		if strings.TrimSpace(entry.Why) == "" {
			t.Errorf("glob %q needs a why", entry.Glob)
		}
		if len(entry.Why) > 200 {
			t.Errorf("glob %q has a why of %d characters, the limit is 200", entry.Glob, len(entry.Why))
		}
	}
	for _, required := range []string{".github/risk-paths.json", ".github/workflows/risk-paths.yml"} {
		if !seen[required] {
			t.Errorf("risk-paths.json must list %q", required)
		}
	}

	caller := readRepoFile(t, ".github/workflows/risk-paths.yml")
	callerRef := regexp.MustCompile(`uses: frostyard/core/\.github/workflows/risk-paths\.yml@[0-9a-f]{40}`)
	if !callerRef.MatchString(caller) {
		t.Error(".github/workflows/risk-paths.yml must call frostyard/core's workflow at a 40-character SHA")
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

// globMatch reports whether a risk glob matches a whole repository path.
// The supported subset is `*` (within one segment), `**` (any segments),
// `**/` (zero or more directories), `?` (one character) and one level of
// `{a,b}` alternation.
func globMatch(glob, name string) bool {
	pattern, err := globRegexp(glob)
	if err != nil {
		return false
	}
	return pattern.MatchString(name)
}

func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString(`(?:[^/]+/)*`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '{':
			end := strings.IndexByte(glob[i:], '}')
			if end < 0 {
				return nil, os.ErrInvalid
			}
			alternatives := strings.Split(glob[i+1:i+end], ",")
			b.WriteString("(?:")
			for j, alternative := range alternatives {
				if j > 0 {
					b.WriteString("|")
				}
				b.WriteString(regexp.QuoteMeta(alternative))
			}
			b.WriteString(")")
			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
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
	for i, line := range lines {
		if line.indent != 0 || line.text != "on:" {
			continue
		}
		for j := i + 1; j < len(lines) && lines[j].indent > 0; j++ {
			if strings.HasPrefix(lines[j].text, "pull_request:") {
				triggers = true
			}
		}
	}
	if !triggers {
		t.Error("ci.yml must run on pull_request")
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
