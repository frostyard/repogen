package deb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/frostyard/repogen/internal/models"
)

const (
	productionReleaseOrigin = "Repogen Repository"
	productionReleaseLabel  = "Frostyard Repository"
)

var productionChecksumLengths = map[string]int{
	"MD5Sum": 32,
	"SHA1":   40,
	"SHA256": 64,
	"SHA512": 128,
}

var (
	productionPackageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	productionVersionPattern     = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~-]*$`)
	productionFieldNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
)

// ProductionState is prior Debian metadata that passed the production
// signature, identity, checksum, and strict-index verification boundary.
type ProductionState struct {
	ReleaseSHA256         string
	SigningKeyFingerprint string
	Packages              []models.Package
	objects               map[string]ProductionObjectDigest
	poolDigests           map[string]PoolDigest
	verified              bool
}

type releaseChecksum struct {
	digest string
	size   int64
}

type productionRelease struct {
	fields    map[string]string
	checksums map[string]map[string]releaseChecksum
}

// VerifyProductionInitialization proves that the requested suite target does
// not exist. It does not create the target or any parent directory.
func VerifyProductionInitialization(outputDir, codename string) error {
	target := filepath.Join(outputDir, "dists", codename)
	_, err := os.Lstat(target)
	switch {
	case err == nil:
		return fmt.Errorf("production initialize target %q already exists", target)
	case os.IsNotExist(err):
		return nil
	default:
		return fmt.Errorf("cannot establish authoritative absence of production target %q: %w", target, err)
	}
}

// RestoreProductionState verifies and parses one complete signed production
// suite without modifying it.
func RestoreProductionState(
	config *models.RepositoryConfig,
	trustedPublicKeyPath string,
	expectedReleaseSHA256 string,
) (*ProductionState, error) {
	if len(expectedReleaseSHA256) != sha256.Size*2 || !isLowerHex(expectedReleaseSHA256) {
		return nil, fmt.Errorf("expected prior Release SHA-256 must be 64 lowercase hexadecimal characters")
	}

	keyring, err := readProductionKeyRing(trustedPublicKeyPath)
	if err != nil {
		return nil, err
	}

	suiteDir := filepath.Join(config.OutputDir, "dists", config.Codename)
	releaseData, err := readProductionMetadata(filepath.Join(suiteDir, "Release"))
	if err != nil {
		return nil, err
	}
	inReleaseData, err := readProductionMetadata(filepath.Join(suiteDir, "InRelease"))
	if err != nil {
		return nil, err
	}
	releaseSignature, err := readProductionMetadata(filepath.Join(suiteDir, "Release.gpg"))
	if err != nil {
		return nil, err
	}

	releaseDigest := sha256.Sum256(releaseData)
	observedReleaseSHA256 := hex.EncodeToString(releaseDigest[:])
	if observedReleaseSHA256 != expectedReleaseSHA256 {
		return nil, fmt.Errorf(
			"prior Release SHA-256 %s does not match expected %s",
			observedReleaseSHA256,
			expectedReleaseSHA256,
		)
	}

	fingerprint, err := verifyProductionReleaseSignatures(
		keyring,
		releaseData,
		inReleaseData,
		releaseSignature,
	)
	if err != nil {
		return nil, err
	}

	release, err := parseProductionRelease(releaseData)
	if err != nil {
		return nil, err
	}
	if err := verifyProductionReleaseIdentity(release, config); err != nil {
		return nil, err
	}

	packages, indexObjects, err := verifyProductionIndexes(suiteDir, release, config.Arches)
	if err != nil {
		return nil, err
	}

	objects := map[string]ProductionObjectDigest{
		path.Join("dists", config.Codename, "Release"): {
			SHA256: observedReleaseSHA256,
			Size:   int64(len(releaseData)),
		},
		path.Join("dists", config.Codename, "InRelease"):   productionDigestForBytes(inReleaseData),
		path.Join("dists", config.Codename, "Release.gpg"): productionDigestForBytes(releaseSignature),
	}
	for relativePath, digest := range indexObjects {
		objects[path.Join("dists", config.Codename, relativePath)] = digest
	}
	poolDigests := make(map[string]PoolDigest, len(packages))
	for _, pkg := range packages {
		poolDigests[pkg.Filename] = PoolDigest{
			SHA256: pkg.SHA256Sum,
			Size:   pkg.Size,
		}
	}

	return &ProductionState{
		ReleaseSHA256:         observedReleaseSHA256,
		SigningKeyFingerprint: fingerprint,
		Packages:              packages,
		objects:               objects,
		poolDigests:           poolDigests,
		verified:              true,
	}, nil
}

func readProductionKeyRing(keyPath string) (openpgp.EntityList, error) {
	data, err := readProductionMetadata(keyPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read trusted production public key: %w", err)
	}

	entities, armoredErr := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	if armoredErr != nil {
		entities, err = openpgp.ReadKeyRing(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf(
				"cannot parse trusted production public key as armored (%v) or binary: %w",
				armoredErr,
				err,
			)
		}
	}
	if len(entities) != 1 {
		return nil, fmt.Errorf("trusted production public key file must contain exactly one key, got %d", len(entities))
	}
	if entities[0].PrivateKey != nil {
		return nil, fmt.Errorf("trusted production public key file contains private key material")
	}
	for _, subkey := range entities[0].Subkeys {
		if subkey.PrivateKey != nil {
			return nil, fmt.Errorf("trusted production public key file contains private subkey material")
		}
	}
	return entities, nil
}

func verifyProductionReleaseSignatures(
	keyring openpgp.EntityList,
	releaseData []byte,
	inReleaseData []byte,
	releaseSignature []byte,
) (string, error) {
	block, rest := clearsign.Decode(inReleaseData)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return "", fmt.Errorf("prior InRelease is not one complete clear-signed message")
	}
	clearSigner, err := block.VerifySignature(keyring, nil)
	if err != nil {
		return "", fmt.Errorf("prior InRelease signature is invalid: %w", err)
	}
	if !bytes.Equal(block.Plaintext, releaseData) {
		return "", fmt.Errorf("prior InRelease signed payload is not byte-identical to Release")
	}

	detachedSigner, err := openpgp.CheckArmoredDetachedSignature(
		keyring,
		bytes.NewReader(releaseData),
		bytes.NewReader(releaseSignature),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("prior Release.gpg signature is invalid: %w", err)
	}

	clearFingerprint := fmt.Sprintf("%X", clearSigner.PrimaryKey.Fingerprint)
	detachedFingerprint := fmt.Sprintf("%X", detachedSigner.PrimaryKey.Fingerprint)
	if clearFingerprint != detachedFingerprint {
		return "", fmt.Errorf(
			"prior signatures disagree on signing key: InRelease %s, Release.gpg %s",
			clearFingerprint,
			detachedFingerprint,
		)
	}
	return clearFingerprint, nil
}

func parseProductionRelease(data []byte) (*productionRelease, error) {
	release := &productionRelease{
		fields:    make(map[string]string),
		checksums: make(map[string]map[string]releaseChecksum),
	}

	var section string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if line == "" {
			return nil, fmt.Errorf("prior Release contains an unexpected blank line at line %d", lineNumber)
		}

		if line[0] == ' ' || line[0] == '\t' {
			if section == "" {
				return nil, fmt.Errorf("prior Release has an unexpected continuation at line %d", lineNumber)
			}
			parts := strings.Fields(line)
			if len(parts) != 3 {
				return nil, fmt.Errorf("prior Release has a malformed %s entry at line %d", section, lineNumber)
			}
			if len(parts[0]) != productionChecksumLengths[section] || !isLowerHex(parts[0]) {
				return nil, fmt.Errorf("prior Release has an invalid %s digest for %q", section, parts[2])
			}
			size, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || size < 0 {
				return nil, fmt.Errorf("prior Release has an invalid size for %q", parts[2])
			}
			if err := validateProductionIndexPath(parts[2]); err != nil {
				return nil, err
			}
			if _, duplicate := release.checksums[section][parts[2]]; duplicate {
				return nil, fmt.Errorf("prior Release repeats %s path %q", section, parts[2])
			}
			release.checksums[section][parts[2]] = releaseChecksum{
				digest: parts[0],
				size:   size,
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != parts[0] {
			return nil, fmt.Errorf("prior Release has a malformed field at line %d", lineNumber)
		}
		name := parts[0]
		value := strings.TrimSpace(parts[1])
		if containsProductionControl(name) || containsProductionControl(value) {
			return nil, fmt.Errorf("prior Release field at line %d contains a control character", lineNumber)
		}
		if _, checksumSection := productionChecksumLengths[name]; checksumSection {
			if value != "" {
				return nil, fmt.Errorf("prior Release checksum section %s has trailing content", name)
			}
			if _, duplicate := release.checksums[name]; duplicate {
				return nil, fmt.Errorf("prior Release repeats checksum section %s", name)
			}
			release.checksums[name] = make(map[string]releaseChecksum)
			section = name
			continue
		}

		section = ""
		if _, duplicate := release.fields[name]; duplicate {
			return nil, fmt.Errorf("prior Release repeats field %s", name)
		}
		switch name {
		case "Origin", "Label", "Suite", "Codename", "Architectures", "Components", "Date", "Acquire-By-Hash":
			release.fields[name] = value
		case "Valid-Until":
			return nil, fmt.Errorf("prior Release unexpectedly contains Valid-Until")
		default:
			return nil, fmt.Errorf("prior Release contains unsupported field %s", name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cannot parse prior Release: %w", err)
	}
	return release, nil
}

func verifyProductionReleaseIdentity(release *productionRelease, config *models.RepositoryConfig) error {
	requiredArchitectures := ProductionArchitectures()
	if strings.Join(config.Arches, ",") != strings.Join(requiredArchitectures, ",") {
		return fmt.Errorf(
			"production restore architecture contract mismatch: got %s, want %s",
			strings.Join(config.Arches, ","),
			strings.Join(requiredArchitectures, ","),
		)
	}
	expectedFields := map[string]string{
		"Origin":          productionReleaseOrigin,
		"Label":           productionReleaseLabel,
		"Suite":           config.Codename,
		"Codename":        config.Codename,
		"Architectures":   strings.Join(config.Arches, " "),
		"Components":      "main",
		"Acquire-By-Hash": "yes",
	}
	for name, expected := range expectedFields {
		observed, ok := release.fields[name]
		if !ok {
			return fmt.Errorf("prior Release is missing required field %s", name)
		}
		if observed != expected {
			return fmt.Errorf("prior Release field %s is %q, want %q", name, observed, expected)
		}
	}
	date, ok := release.fields["Date"]
	if !ok {
		return fmt.Errorf("prior Release is missing required field Date")
	}
	if _, err := time.Parse(time.RFC1123Z, date); err != nil {
		return fmt.Errorf("prior Release Date %q is invalid: %w", date, err)
	}
	return nil
}

func verifyProductionIndexes(
	suiteDir string,
	release *productionRelease,
	architectures []string,
) ([]models.Package, map[string]ProductionObjectDigest, error) {
	expectedPaths := make(map[string]string, len(architectures)*2)
	for _, architecture := range architectures {
		base := path.Join("main", "binary-"+architecture, "Packages")
		expectedPaths[base] = architecture
		expectedPaths[base+".gz"] = architecture
	}

	for section := range productionChecksumLengths {
		entries, ok := release.checksums[section]
		if !ok {
			return nil, nil, fmt.Errorf("prior Release is missing checksum section %s", section)
		}
		if len(entries) != len(expectedPaths) {
			return nil, nil, fmt.Errorf(
				"prior Release section %s advertises %d indexes, want %d",
				section,
				len(entries),
				len(expectedPaths),
			)
		}
		for expectedPath := range expectedPaths {
			if _, ok := entries[expectedPath]; !ok {
				return nil, nil, fmt.Errorf("prior Release section %s is missing %q", section, expectedPath)
			}
		}
	}

	verifiedBytes := make(map[string][]byte, len(expectedPaths))
	objectDigests := make(map[string]ProductionObjectDigest, len(expectedPaths))
	for indexPath := range expectedPaths {
		data, err := readProductionMetadata(filepath.Join(suiteDir, filepath.FromSlash(indexPath)))
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read prior index %q: %w", indexPath, err)
		}
		for section := range productionChecksumLengths {
			if err := verifyReleaseChecksum(section, release.checksums[section][indexPath], data); err != nil {
				return nil, nil, fmt.Errorf("prior index %q failed %s verification: %w", indexPath, section, err)
			}
		}
		byHashPath := path.Join(
			path.Dir(indexPath),
			"by-hash",
			"SHA256",
			release.checksums["SHA256"][indexPath].digest,
		)
		byHashData, err := readProductionMetadata(filepath.Join(suiteDir, filepath.FromSlash(byHashPath)))
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read prior by-hash index %q: %w", byHashPath, err)
		}
		if !bytes.Equal(byHashData, data) {
			return nil, nil, fmt.Errorf("prior by-hash index %q is not byte-identical to %q", byHashPath, indexPath)
		}
		verifiedBytes[indexPath] = data
		objectDigests[indexPath] = productionDigestForBytes(data)
	}

	var packages []models.Package
	seenFilenames := make(map[string]struct{})
	for _, architecture := range architectures {
		plainPath := path.Join("main", "binary-"+architecture, "Packages")
		gzipPath := plainPath + ".gz"
		uncompressed, err := decompressProductionIndex(verifiedBytes[gzipPath])
		if err != nil {
			return nil, nil, fmt.Errorf("prior index %q is invalid: %w", gzipPath, err)
		}
		if !bytes.Equal(uncompressed, verifiedBytes[plainPath]) {
			return nil, nil, fmt.Errorf("prior indexes %q and %q are not byte-identical after decompression", plainPath, gzipPath)
		}

		indexPackages, err := parseProductionPackages(verifiedBytes[plainPath], architecture)
		if err != nil {
			return nil, nil, fmt.Errorf("prior index %q cannot be parsed: %w", plainPath, err)
		}
		for _, pkg := range indexPackages {
			if _, duplicate := seenFilenames[pkg.Filename]; duplicate {
				return nil, nil, fmt.Errorf("prior package path %q is advertised more than once", pkg.Filename)
			}
			seenFilenames[pkg.Filename] = struct{}{}
			packages = append(packages, pkg)
		}
	}
	return packages, objectDigests, nil
}

func productionDigestForBytes(data []byte) ProductionObjectDigest {
	digest := sha256.Sum256(data)
	return ProductionObjectDigest{
		SHA256: hex.EncodeToString(digest[:]),
		Size:   int64(len(data)),
	}
}

func verifyReleaseChecksum(section string, expected releaseChecksum, data []byte) error {
	if expected.size != int64(len(data)) {
		return fmt.Errorf("size is %d, want %d", len(data), expected.size)
	}

	var observed string
	switch section {
	case "MD5Sum":
		digest := md5.Sum(data)
		observed = hex.EncodeToString(digest[:])
	case "SHA1":
		digest := sha1.Sum(data)
		observed = hex.EncodeToString(digest[:])
	case "SHA256":
		digest := sha256.Sum256(data)
		observed = hex.EncodeToString(digest[:])
	case "SHA512":
		digest := sha512.Sum512(data)
		observed = hex.EncodeToString(digest[:])
	default:
		return fmt.Errorf("unsupported checksum section %s", section)
	}
	if observed != expected.digest {
		return fmt.Errorf("digest is %s, want %s", observed, expected.digest)
	}
	return nil
}

func decompressProductionIndex(data []byte) ([]byte, error) {
	source := bytes.NewReader(data)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, err
	}
	reader.Multistream(false)
	uncompressed, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if source.Len() != 0 {
		return nil, fmt.Errorf("gzip stream has %d trailing bytes", source.Len())
	}
	return uncompressed, nil
}

func parseProductionPackages(data []byte, expectedArchitecture string) ([]models.Package, error) {
	var packages []models.Package
	fields := make(map[string]string)
	var currentField string

	finishStanza := func() error {
		if len(fields) == 0 {
			return nil
		}
		pkg, err := productionPackageFromFields(fields, expectedArchitecture)
		if err != nil {
			return fmt.Errorf("package stanza %d: %w", len(packages)+1, err)
		}
		packages = append(packages, pkg)
		fields = make(map[string]string)
		currentField = ""
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if line == "" {
			if err := finishStanza(); err != nil {
				return nil, err
			}
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if currentField == "" {
				return nil, fmt.Errorf("unexpected continuation at line %d", lineNumber)
			}
			fields[currentField] += "\n" + strings.TrimSpace(line)
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || parts[0] == "" || strings.TrimSpace(parts[0]) != parts[0] {
			return nil, fmt.Errorf("malformed field at line %d", lineNumber)
		}
		if _, duplicate := fields[parts[0]]; duplicate {
			return nil, fmt.Errorf("duplicate field %s at line %d", parts[0], lineNumber)
		}
		currentField = parts[0]
		fields[currentField] = strings.TrimSpace(parts[1])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := finishStanza(); err != nil {
		return nil, err
	}
	return packages, nil
}

func productionPackageFromFields(fields map[string]string, expectedArchitecture string) (models.Package, error) {
	required := []string{
		"Package",
		"Version",
		"Architecture",
		"Filename",
		"Size",
		"MD5sum",
		"SHA1",
		"SHA256",
		"SHA512",
	}
	for _, field := range required {
		if fields[field] == "" {
			return models.Package{}, fmt.Errorf("missing required field %s", field)
		}
	}
	if fields["Architecture"] != expectedArchitecture {
		return models.Package{}, fmt.Errorf(
			"architecture is %q, want %q",
			fields["Architecture"],
			expectedArchitecture,
		)
	}
	if err := validateProductionPoolPath(fields["Filename"]); err != nil {
		return models.Package{}, err
	}
	if !productionPackageNamePattern.MatchString(fields["Package"]) {
		return models.Package{}, fmt.Errorf("invalid Package %q", fields["Package"])
	}
	if !productionVersionPattern.MatchString(fields["Version"]) {
		return models.Package{}, fmt.Errorf("invalid Version %q", fields["Version"])
	}

	size, err := strconv.ParseInt(fields["Size"], 10, 64)
	if err != nil || size < 0 {
		return models.Package{}, fmt.Errorf("invalid Size %q", fields["Size"])
	}
	for field, length := range map[string]int{
		"MD5sum": 32,
		"SHA1":   40,
		"SHA256": 64,
		"SHA512": 128,
	} {
		if len(fields[field]) != length || !isLowerHex(fields[field]) {
			return models.Package{}, fmt.Errorf("invalid %s digest", field)
		}
	}

	pkg := models.Package{
		Name:         fields["Package"],
		Version:      fields["Version"],
		Architecture: fields["Architecture"],
		Filename:     fields["Filename"],
		Size:         size,
		MD5Sum:       fields["MD5sum"],
		SHA1Sum:      fields["SHA1"],
		SHA256Sum:    fields["SHA256"],
		SHA512Sum:    fields["SHA512"],
		Maintainer:   fields["Maintainer"],
		Homepage:     fields["Homepage"],
		Description:  fields["Description"],
		Metadata:     make(map[string]interface{}),
	}
	if fields["Depends"] != "" {
		for _, dependency := range strings.Split(fields["Depends"], ",") {
			pkg.Dependencies = append(pkg.Dependencies, strings.TrimSpace(dependency))
		}
	}

	known := map[string]struct{}{
		"Package": {}, "Version": {}, "Architecture": {}, "Filename": {},
		"Size": {}, "MD5sum": {}, "SHA1": {}, "SHA256": {}, "SHA512": {},
		"Maintainer": {}, "Homepage": {}, "Description": {}, "Depends": {},
	}
	for name, value := range fields {
		if !productionFieldNamePattern.MatchString(name) {
			return models.Package{}, fmt.Errorf("invalid metadata field name %q", name)
		}
		if _, ok := known[name]; ok {
			continue
		}
		if containsProductionControl(value) {
			return models.Package{}, fmt.Errorf("metadata field %q contains a control character", name)
		}
		pkg.Metadata[name] = value
	}
	for name, value := range map[string]string{
		"Maintainer": fields["Maintainer"],
		"Homepage":   fields["Homepage"],
		"Depends":    fields["Depends"],
	} {
		if containsProductionControl(value) {
			return models.Package{}, fmt.Errorf("field %s contains a control character", name)
		}
	}
	if strings.IndexFunc(fields["Description"], func(char rune) bool {
		return unicode.IsControl(char) && char != '\n' && char != '\t'
	}) >= 0 {
		return models.Package{}, fmt.Errorf("field Description contains a disallowed control character")
	}
	return pkg, nil
}

func validateProductionIndexPath(indexPath string) error {
	if strings.Contains(indexPath, `\`) || path.IsAbs(indexPath) || path.Clean(indexPath) != indexPath {
		return fmt.Errorf("prior Release advertises unsafe index path %q", indexPath)
	}
	parts := strings.Split(indexPath, "/")
	if len(parts) != 3 ||
		parts[0] != "main" ||
		!strings.HasPrefix(parts[1], "binary-") ||
		(parts[2] != "Packages" && parts[2] != "Packages.gz") {
		return fmt.Errorf("prior Release advertises unsupported index path %q", indexPath)
	}
	return nil
}

func validateProductionPoolPath(poolPath string) error {
	if strings.Contains(poolPath, `\`) ||
		path.IsAbs(poolPath) ||
		path.Clean(poolPath) != poolPath ||
		!strings.HasPrefix(poolPath, "pool/main/") {
		return fmt.Errorf("package Filename %q is not a safe shared-pool path", poolPath)
	}
	if strings.IndexFunc(poolPath, unicode.IsControl) >= 0 {
		return fmt.Errorf("package Filename contains a control character")
	}
	return nil
}

func readProductionMetadata(filePath string) ([]byte, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%q is a symlink", filePath)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("%q changed while it was being opened", filePath)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	finalInfo, err := os.Lstat(filePath)
	if err != nil {
		return nil, fmt.Errorf("%q changed while it was being read: %w", filePath, err)
	}
	if finalInfo.Mode()&os.ModeSymlink != 0 ||
		!finalInfo.Mode().IsRegular() ||
		!os.SameFile(openedInfo, finalInfo) {
		return nil, fmt.Errorf("%q changed while it was being read", filePath)
	}
	return data, nil
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func containsProductionControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
