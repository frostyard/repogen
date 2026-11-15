// The corrections gate (core ADR-0018, ADR-0081): `make ci` runs
// scripts/check-corrections.mjs — the format of every record in
// .memory/corrections/, and with BASE the append-only comparison against the
// base commit.
//
// The script is a byte-identical copy of core's, so it is never edited here;
// this guard holds the wiring around it: the tracked script, the Makefile
// targets that `make ci` reaches, the refusal of a BASE that does not resolve
// to a commit, and the workflow's base commit plus the full-history checkout
// that puts it in the clone.
package baseline

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// correctionsScript is the corrections validator, copied from frostyard/core.
const correctionsScript = "scripts/check-corrections.mjs"

// correctionsGateTree is the part of the repository the corrections gate
// lives in, as text, so a test can remove 1 piece and watch the guard fail.
type correctionsGateTree struct {
	script   bool
	makefile string
	workflow string
}

func TestRepositoryBaselineCorrectionsGate(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot(t), correctionsScript)); err != nil {
		t.Fatalf("%s: %v", correctionsScript, err)
	}
	tree := correctionsGateTree{
		script:   true,
		makefile: readRepoFile(t, "Makefile"),
		workflow: readRepoFile(t, ".github/workflows/ci.yml"),
	}
	for _, problem := range correctionsGateProblems(tree) {
		t.Errorf("%s", problem)
	}
}

// correctionsGateProblems checks the corrections gate of tree and names 1
// problem for each rule it breaks.
func correctionsGateProblems(tree correctionsGateTree) []string {
	if !tree.script {
		return []string{correctionsScript + " is missing: `make ci` runs it (core ADR-0081)"}
	}

	rules := rulePrerequisites(tree.makefile)
	bodies := makefileBodies(tree.makefile)
	var problems []string

	for _, target := range []string{"corrections-check", "corrections-base"} {
		if _, ok := rules[target]; !ok {
			problems = append(problems, "the Makefile has no "+target+": rule")
			continue
		}
		if !makeTargetsRun(tree.makefile, target) {
			if target == "corrections-check" {
				problems = append(problems, "`make ci` must run corrections-check: it must be a prerequisite of verify-static, or of the ci: rule")
				continue
			}
			problems = append(problems, "`make ci` must run corrections-base: it must be a prerequisite of the ci: rule")
		}
	}

	if strings.Join(bodyLines(bodies["corrections-check"]), "\n") == "" {
		problems = append(problems, "the corrections-check: rule has no recipe")
	} else if !strings.Contains(strings.Join(bodies["corrections-check"], "\n"), "node "+correctionsScript) {
		problems = append(problems, "the corrections-check: rule must run `node "+correctionsScript+"`")
	}

	// A BASE that does not resolve must fail loudly: a typo or an unfetched
	// ref must never turn the append-only comparison into a silent pass.
	base := strings.Join(bodies["corrections-base"], "\n")
	if !strings.Contains(base, "rev-parse --verify") || !strings.Contains(base, "does not resolve to a commit") {
		problems = append(problems, "the corrections-base: rule must refuse a BASE that does not resolve to a commit")
	}
	if !strings.Contains(base, correctionsScript) || !strings.Contains(base, "--base") {
		problems = append(problems, "the corrections-base: rule must run `node "+correctionsScript+" --base` when BASE holds the script")
	}

	problems = append(problems, correctionsWorkflowProblems(tree.workflow)...)
	return problems
}

// makefileBodies maps each Makefile target to the recipe lines under its rule
// (the leading tab is removed).
func makefileBodies(makefile string) map[string][]string {
	bodies := make(map[string][]string)
	current := ""
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") {
			if current != "" {
				bodies[current] = append(bodies[current], strings.TrimPrefix(line, "\t"))
			}
			continue
		}
		current = ruleTarget(line)
	}
	return bodies
}

// ruleTarget returns the target of a Makefile rule line, or "" for a comment,
// a variable assignment, a directive or any other line.
func ruleTarget(line string) string {
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\t") {
		return ""
	}
	colon := strings.Index(line, ":")
	if colon <= 0 {
		return ""
	}
	target := strings.TrimSpace(line[:colon])
	if target == "" || strings.ContainsAny(target, " \t") || strings.Contains(target, "=") {
		return ""
	}
	return target
}

// makeTargetsRun reports whether `make ci` would run target: target is 1 of
// its prerequisites, followed transitively, or 1 of the prerequisites is a
// recipe that runs `make target`.
func makeTargetsRun(makefile, target string) bool {
	rules := rulePrerequisites(makefile)
	bodies := makefileBodies(makefile)
	seen := map[string]bool{}
	queue := []string{"ci"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if name == target {
			return true
		}
		for _, line := range bodies[name] {
			if slices.Contains(strings.Fields(line), target) {
				return true
			}
		}
		queue = append(queue, rules[name]...)
	}
	return false
}

// bodyLines drops the empty lines, so a rule with no recipe reads as empty.
func bodyLines(lines []string) []string {
	var kept []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return kept
}

// baseExpression is the base commit of a pull request, as the workflow reads
// it; on a push or a merge-group event it is empty.
const baseExpression = "${{ github.event.pull_request.base.sha }}"

// correctionsWorkflowProblems checks the CI workflow: the step that runs
// `make ci` must pass the base commit as BASE, and the checkout step must
// fetch the full history, so that the base commit is in the clone that
// corrections-base compares against.
func correctionsWorkflowProblems(workflow string) []string {
	var problems []string
	gate := false
	checkout := false
	for _, block := range stepBlocks(workflow) {
		keyIndent := stepKeyIndent(block)
		if runs := blockRun(block); runs == "make ci" {
			gate = true
			value, ok := blockChild(block, keyIndent, "env", "BASE")
			if !ok {
				problems = append(problems, "the step that runs `make ci` must set env BASE: "+baseExpression)
			} else if value != baseExpression {
				problems = append(problems, "the step that runs `make ci` must set env BASE: "+baseExpression+", got "+value)
			}
		}
		uses, _ := blockChild(block, keyIndent, "", "uses")
		if strings.HasPrefix(uses, "actions/checkout@") {
			checkout = true
			value, ok := blockChild(block, keyIndent, "with", "fetch-depth")
			if !ok || value != "0" {
				problems = append(problems, "the actions/checkout step must set with fetch-depth: 0, so corrections-base can compare against BASE")
			}
		}
	}
	if !gate {
		problems = append(problems, "the workflow has no step that runs `make ci`")
	}
	if !checkout {
		problems = append(problems, "the workflow has no actions/checkout step")
	}
	return problems
}

// stepBlocks returns each `- ` block of the workflow (1 step of a job), with
// the lines of its nested blocks.
func stepBlocks(text string) [][]yamlLine {
	lines := yamlLines(text)
	var blocks [][]yamlLine
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i].text, "- ") {
			continue
		}
		indent := lines[i].indent
		end := i + 1
		for end < len(lines) {
			next := lines[end]
			if next.indent < indent || (next.indent == indent && strings.HasPrefix(next.text, "- ")) {
				break
			}
			end++
		}
		blocks = append(blocks, lines[i:end])
		i = end - 1
	}
	return blocks
}

// stepKeyIndent is the indentation of a step's own keys: 2 more than the
// `- name: ...` line that starts it.
func stepKeyIndent(block []yamlLine) int {
	if len(block) == 0 {
		return 0
	}
	return block[0].indent + 2
}

// blockRun returns the value of the step's run: key, or "".
func blockRun(block []yamlLine) string {
	for _, line := range block {
		if strings.HasPrefix(line.text, "#") {
			continue
		}
		if value, ok := yamlValue(line.text, "run"); ok {
			return value
		}
	}
	return ""
}

// blockChild returns the value of key under block, either a key of the step
// itself (parent "") or a key inside its parent: block (`with:`, `env:`).
func blockChild(block []yamlLine, keyIndent int, parent, key string) (string, bool) {
	childIndent := -1
	inParent := parent == ""
	for _, line := range block {
		if strings.HasPrefix(line.text, "#") {
			continue
		}
		indent := line.indent
		if strings.HasPrefix(line.text, "- ") {
			indent += 2
		}
		if inParent {
			if indent <= keyIndent {
				inParent = parent == "" && childIndent < 0
			}
			if childIndent < 0 {
				childIndent = indent
			}
			if indent == childIndent {
				if value, ok := yamlValue(line.text, key); ok {
					return value, true
				}
			}
			continue
		}
		if indent == keyIndent && line.text == parent+":" {
			inParent = true
		}
	}
	return "", false
}

func TestCorrectionsGateProblems(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	workflow := readRepoFile(t, ".github/workflows/ci.yml")

	cases := []struct {
		name     string
		tree     correctionsGateTree
		problems []string
	}{
		{
			name: "the gate as written",
			tree: correctionsGateTree{script: true, makefile: makefile, workflow: workflow},
		},
		{
			name: "the script is missing",
			tree: correctionsGateTree{script: false, makefile: makefile, workflow: workflow},
			problems: []string{
				correctionsScript + " is missing: `make ci` runs it (core ADR-0081)",
			},
		},
		{
			name: "verify-static no longer runs corrections-check",
			tree: correctionsGateTree{
				script:   true,
				makefile: strings.Replace(makefile, "verify-static: tidy-check vet fmt-check lint docs-check corrections-check", "verify-static: tidy-check vet fmt-check lint docs-check", 1),
				workflow: workflow,
			},
			problems: []string{
				"`make ci` must run corrections-check: it must be a prerequisite of verify-static, or of the ci: rule",
			},
		},
		{
			name: "the ci rule no longer runs corrections-base",
			tree: correctionsGateTree{
				script:   true,
				makefile: strings.Replace(makefile, "ci: verify corrections-base vuln", "ci: verify vuln", 1),
				workflow: workflow,
			},
			problems: []string{
				"`make ci` must run corrections-base: it must be a prerequisite of the ci: rule",
			},
		},
		{
			name: "corrections-base no longer refuses an unresolvable BASE",
			tree: correctionsGateTree{
				script: true,
				makefile: strings.Replace(makefile,
					"\tif ! git rev-parse --verify --quiet \"$(BASE)^{commit}\" >/dev/null; then \\\n\t\techo \"FAIL: BASE $(BASE) does not resolve to a commit (fetch it first)\"; \\\n\t\texit 1; \\\n\tfi; \\\n",
					"", 1),
				workflow: workflow,
			},
			problems: []string{
				"the corrections-base: rule must refuse a BASE that does not resolve to a commit",
			},
		},
		{
			name: "the workflow step passes no BASE",
			tree: correctionsGateTree{
				script:   true,
				makefile: makefile,
				workflow: strings.Replace(workflow, "        env:\n          BASE: "+baseExpression+"\n", "", 1),
			},
			problems: []string{
				"the step that runs `make ci` must set env BASE: " + baseExpression,
			},
		},
		{
			name: "the checkout fetches no history",
			tree: correctionsGateTree{
				script:   true,
				makefile: makefile,
				workflow: strings.Replace(workflow, "          fetch-depth: 0\n", "", 1),
			},
			problems: []string{
				"the actions/checkout step must set with fetch-depth: 0, so corrections-base can compare against BASE",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := correctionsGateProblems(testCase.tree)
			if !slices.Equal(got, testCase.problems) {
				t.Fatalf("problems = %q, want %q", got, testCase.problems)
			}
		})
	}
}
