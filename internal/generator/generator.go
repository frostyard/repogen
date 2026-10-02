package generator

import (
	"context"
	"errors"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
)

// Generator interface for repository generators
type Generator interface {
	// Generate creates a repository structure from the provided packages
	Generate(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error

	// ValidatePackages checks if packages are valid for this generator
	ValidatePackages(packages []models.Package) error

	// GetSupportedType returns the package type this generator supports
	GetSupportedType() scanner.PackageType

	// ParseExistingMetadata reads existing repository metadata and returns packages already in the repo
	ParseExistingMetadata(config *models.RepositoryConfig) ([]models.Package, error)
}

// ErrNoExistingMetadata reports that an incremental run found no prior
// repository metadata at all, so it may initialize a fresh repository.
var ErrNoExistingMetadata = errors.New("no existing repository metadata")
