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

	// Output options
	HTMLIndex bool // Generate HTML directory index pages for browser-friendly navigation
}
