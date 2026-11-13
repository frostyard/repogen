package models

// RepositoryConfig contains configuration for repository generation
type RepositoryConfig struct {
	// Input/Output
	InputDir  string
	OutputDir string

	// Repository metadata
	Origin     string
	Label      string
	RepoName   string   // Repository name for Pacman .db files
	Codename   string   // For Debian
	Suite      string   // For Debian
	Components []string // For Debian (main, contrib, etc.)
	Arches     []string // Architectures to support

	// Signing
	GPGKeyPath    string
	GPGPassphrase string
	RSAKeyPath    string
	RSAPassphrase string
	RSAKeyName    string // For Alpine

	// Type-specific options
	BaseURL string // For Homebrew bottles and sysext transfer files

	// Incremental mode
	Incremental    bool // Add new packages to existing repository without removing existing ones
	SkipDuplicates bool // In incremental mode, skip packages that already exist instead of failing

	// SkipPublishedIdentities makes incremental sysext runs skip each incoming
	// sysext whose identity (name, version, OSVersion, arch) the
	// signature-verified SHA256SUMS already lists, whatever its bytes or
	// compression suffix. Rebuilt mkosi images are not byte-reproducible, so
	// without this option such a rebuild fails the whole run (ADR-0018).
	SkipPublishedIdentities bool

	// SkippedReportPath, when set with SkipPublishedIdentities, receives the
	// published payload path (ext/NAME/FILE) of each skipped sysext, one per
	// line, sorted. The file is written only when the run succeeds.
	SkippedReportPath string

	// SysextKeepVersions, when greater than 0, keeps only the newest N
	// versions of each sysext identity group (name, OS version, arch) in
	// SHA256SUMS, in systemd-sysupdate version order. 0 keeps every version,
	// which is the default: nothing is pruned (ADR-0019).
	SysextKeepVersions int

	// PrunedReportPath, when set with SysextKeepVersions, receives the
	// published payload path (ext/NAME/FILE) of each entry that retention
	// dropped from SHA256SUMS, one per line, sorted. The file is written only
	// when the run succeeds.
	PrunedReportPath string

	// Output options
	HTMLIndex bool // Generate HTML directory index pages for browser-friendly navigation
}
