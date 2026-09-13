package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/buildinfo"
)

func TestWriteVersionIncludesEmbeddedVersionAndCommit(t *testing.T) {
	originalVersion, originalCommit := buildinfo.Version, buildinfo.Commit
	buildinfo.Version = "1.2.3"
	buildinfo.Commit = strings.Repeat("a", 40)
	t.Cleanup(func() {
		buildinfo.Version = originalVersion
		buildinfo.Commit = originalCommit
	})

	var output bytes.Buffer
	if err := writeVersion(&output, true, false); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "1.2.3 "+strings.Repeat("a", 40)+"\n"; got != want {
		t.Fatalf("short version = %q, want %q", got, want)
	}

	output.Reset()
	if err := writeVersion(&output, false, true); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), `{"version":"1.2.3","commit":"`+strings.Repeat("a", 40)+`"}`+"\n"; got != want {
		t.Fatalf("JSON version = %q, want %q", got, want)
	}

	output.Reset()
	root := NewRootCmd()
	root.SetOut(&output)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "repogen version 1.2.3 (commit "+strings.Repeat("a", 40)+")\n"; got != want {
		t.Fatalf("--version output = %q, want %q", got, want)
	}
}
