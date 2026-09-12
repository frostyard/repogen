package deb

import (
	"bytes"
	"testing"
	"time"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
)

func TestGenerateReleaseFileAtCanonicalOrder(t *testing.T) {
	publishedAt := time.Date(2026, time.September, 12, 18, 0, 0, 0, time.FixedZone("other", 3600))
	config := &models.RepositoryConfig{
		Origin:     "Test",
		Label:      "Test",
		Suite:      "trixie",
		Codename:   "trixie",
		Arches:     []string{"amd64", "all"},
		Components: []string{"z", "main"},
	}
	first := ReleaseFileInfo{Path: "z/Packages", Checksum: &utils.Checksum{MD5: "m2", SHA1: "s2", SHA256: "s256-2", SHA512: "s512-2", Size: 2}}
	second := ReleaseFileInfo{Path: "a/Packages", Checksum: &utils.Checksum{MD5: "m1", SHA1: "s1", SHA256: "s256-1", SHA512: "s512-1", Size: 1}}

	got, err := GenerateReleaseFileAt(config, []ReleaseFileInfo{first, second}, publishedAt)
	if err != nil {
		t.Fatal(err)
	}
	shuffledConfig := *config
	shuffledConfig.Arches = []string{"all", "amd64"}
	shuffledConfig.Components = []string{"main", "z"}
	shuffled, err := GenerateReleaseFileAt(&shuffledConfig, []ReleaseFileInfo{second, first}, publishedAt.UTC())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, shuffled) {
		t.Fatalf("shuffled Release inputs produced different bytes:\n%s\n---\n%s", got, shuffled)
	}
}
