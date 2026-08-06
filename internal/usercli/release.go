package usercli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/boundedio"
)

const (
	releaseRepository       = "Christopher-Schulze/reconc"
	releaseAPIBase          = "https://api.github.com/repos/Christopher-Schulze/reconc"
	releaseDownloadBase     = "https://github.com/Christopher-Schulze/reconc/releases/download"
	releaseManifestName     = "release-manifest.json"
	releaseChecksumsName    = "SHA256SUMS"
	releaseManifestFormat   = "reconc.release/v1"
	maxReleaseMetadataBytes = 2 << 20
	maxReleaseAssets        = 128
	maxReleaseList          = 32
	maxReleaseDirEntries    = 512
)

var (
	semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	lifecycleHTTPClient    = &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("release download exceeded five redirects")
			}
			if request.URL.Scheme != "https" {
				return errors.New("release download redirected outside HTTPS")
			}
			return nil
		},
	}
)

type ReleaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	URL    string `json:"-"`
}

type ReleaseManifest struct {
	FormatVersion string         `json:"format_version"`
	Repository    string         `json:"repository"`
	Tag           string         `json:"tag"`
	Version       string         `json:"version"`
	Prerelease    bool           `json:"prerelease"`
	Assets        []ReleaseAsset `json:"assets"`
}

type selectedRelease struct {
	manifest ReleaseManifest
	asset    ReleaseAsset
	localDir string
	channel  Channel
}

type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type semanticVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
	original   string
}

func selectRelease(ctx context.Context, request UpdateRequest) (selectedRelease, error) {
	channel, err := selectedChannel(request)
	if err != nil {
		return selectedRelease{}, err
	}
	if !supportedDirectTarget() {
		return selectedRelease{}, fmt.Errorf("unsupported direct release target %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if strings.TrimSpace(request.FromDir) != "" {
		return selectLocalRelease(request.FromDir, request.Version, channel)
	}
	return selectOnlineRelease(ctx, request.Version, channel)
}

func selectedChannel(request UpdateRequest) (Channel, error) {
	version := strings.TrimSpace(request.Version)
	channel := request.Channel
	if version != "" && channel != "" {
		return "", errors.New("--channel and --version are mutually exclusive")
	}
	if version != "" {
		if _, err := parseSemanticVersion(version); err != nil {
			return "", fmt.Errorf("invalid exact version: %w", err)
		}
		return ChannelExact, nil
	}
	if channel == "" {
		channel = ChannelStable
	}
	if channel != ChannelStable && channel != ChannelPreview {
		return "", fmt.Errorf("update channel must be stable or preview, got %q", channel)
	}
	return channel, nil
}

func selectOnlineRelease(ctx context.Context, exact string, channel Channel) (selectedRelease, error) {
	release, err := discoverOnlineRelease(ctx, strings.TrimSpace(exact), channel)
	if err != nil {
		return selectedRelease{}, err
	}
	version, err := versionFromTag(release.TagName)
	if err != nil {
		return selectedRelease{}, err
	}
	if err := validateReleaseClass(version, release.Draft, release.Prerelease, channel); err != nil {
		return selectedRelease{}, err
	}
	assetName := targetArtifact(version)
	asset, manifestAsset, checksumAsset, err := githubAssets(release, assetName)
	if err != nil {
		return selectedRelease{}, err
	}
	manifestBody, err := downloadBounded(ctx, manifestAsset.BrowserDownloadURL, maxReleaseMetadataBytes)
	if err != nil {
		return selectedRelease{}, fmt.Errorf("download release manifest: %w", err)
	}
	if int64(len(manifestBody)) != manifestAsset.Size {
		return selectedRelease{}, errors.New("release manifest size does not match GitHub metadata")
	}
	manifest, err := decodeReleaseManifest(manifestBody)
	if err != nil {
		return selectedRelease{}, err
	}
	if manifest.Tag != release.TagName || manifest.Version != version || manifest.Prerelease != release.Prerelease {
		return selectedRelease{}, errors.New("release manifest disagrees with GitHub release identity")
	}
	if err := validateOnlineInventory(release, manifest); err != nil {
		return selectedRelease{}, err
	}
	checksums, err := downloadBounded(ctx, checksumAsset.BrowserDownloadURL, maxReleaseMetadataBytes)
	if err != nil {
		return selectedRelease{}, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	if int64(len(checksums)) != checksumAsset.Size {
		return selectedRelease{}, errors.New("SHA256SUMS size does not match GitHub metadata")
	}
	if err := validateChecksumInventory(manifest, manifestBody, checksums); err != nil {
		return selectedRelease{}, err
	}
	selected, ok := releaseAssetByName(manifest, assetName)
	if !ok || selected.Size != asset.Size {
		return selectedRelease{}, errors.New("target asset disagrees with the strict release manifest")
	}
	selected.URL = asset.BrowserDownloadURL
	return selectedRelease{manifest: manifest, asset: selected, channel: channel}, nil
}

func discoverOnlineRelease(ctx context.Context, exact string, channel Channel) (githubRelease, error) {
	switch channel {
	case ChannelStable:
		var release githubRelease
		if err := getJSON(ctx, releaseAPIBase+"/releases/latest", &release); err != nil {
			return githubRelease{}, err
		}
		return release, nil
	case ChannelExact:
		var release githubRelease
		tag := "reconc-v" + exact
		if err := getJSON(ctx, releaseAPIBase+"/releases/tags/"+url.PathEscape(tag), &release); err != nil {
			return githubRelease{}, err
		}
		return release, nil
	case ChannelPreview:
		var releases []githubRelease
		if err := getJSON(ctx, releaseAPIBase+"/releases?per_page=32", &releases); err != nil {
			return githubRelease{}, err
		}
		if len(releases) > maxReleaseList {
			return githubRelease{}, fmt.Errorf("release list exceeds %d entries", maxReleaseList)
		}
		for _, release := range releases {
			if !release.Draft && release.Prerelease {
				return release, nil
			}
		}
		return githubRelease{}, errors.New("no non-draft preview release is available")
	default:
		return githubRelease{}, fmt.Errorf("unsupported release channel %q", channel)
	}
}

func getJSON(ctx context.Context, endpoint string, target interface{}) error {
	body, err := downloadBounded(ctx, endpoint, maxReleaseMetadataBytes)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode GitHub release response: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode GitHub release response: %w", err)
	}
	return nil
}

func downloadBounded(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" {
		return nil, fmt.Errorf("release URL must be valid HTTPS: %q", endpoint)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "reconc-update")
	response, err := lifecycleHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("release endpoint returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("release response exceeds %d bytes", limit)
	}
	return body, nil
}

func githubAssets(release githubRelease, target string) (githubAsset, githubAsset, githubAsset, error) {
	var targetAsset, manifestAsset, checksumsAsset githubAsset
	if len(release.Assets) > maxReleaseAssets {
		return targetAsset, manifestAsset, checksumsAsset, fmt.Errorf("release has more than %d assets", maxReleaseAssets)
	}
	targetCount := 0
	manifestCount := 0
	checksumCount := 0
	seen := map[string]struct{}{}
	for _, asset := range release.Assets {
		if _, exists := seen[asset.Name]; exists {
			return targetAsset, manifestAsset, checksumsAsset, fmt.Errorf("release contains duplicate asset %q", asset.Name)
		}
		seen[asset.Name] = struct{}{}
		expectedURL := releaseDownloadBase + "/" + release.TagName + "/" + asset.Name
		if asset.BrowserDownloadURL != expectedURL {
			return targetAsset, manifestAsset, checksumsAsset, fmt.Errorf("release asset %q has noncanonical immutable URL", asset.Name)
		}
		switch asset.Name {
		case target:
			targetAsset = asset
			targetCount++
		case releaseManifestName:
			manifestAsset = asset
			manifestCount++
		case releaseChecksumsName:
			checksumsAsset = asset
			checksumCount++
		}
	}
	if targetCount != 1 || manifestCount != 1 || checksumCount != 1 ||
		targetAsset.Size <= 0 || manifestAsset.Size <= 0 || checksumsAsset.Size <= 0 {
		return targetAsset, manifestAsset, checksumsAsset,
			fmt.Errorf("release must contain exactly one %s, %s, and %s", target, releaseManifestName, releaseChecksumsName)
	}
	return targetAsset, manifestAsset, checksumsAsset, nil
}

func selectLocalRelease(directory string, exact string, channel Channel) (selectedRelease, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return selectedRelease{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return selectedRelease{}, fmt.Errorf("inspect local release directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return selectedRelease{}, errors.New("local release path must be a real directory")
	}
	manifest, manifestBody, err := loadReleaseManifest(filepath.Join(absolute, releaseManifestName))
	if err != nil {
		return selectedRelease{}, err
	}
	expectedChannel := channel
	if exact != "" {
		expectedChannel = ChannelExact
		if manifest.Version != exact {
			return selectedRelease{}, fmt.Errorf("local release version %s does not match requested %s", manifest.Version, exact)
		}
	}
	if err := validateReleaseClass(manifest.Version, false, manifest.Prerelease, expectedChannel); err != nil {
		return selectedRelease{}, err
	}
	checksums, err := readReleaseMetadata(filepath.Join(absolute, releaseChecksumsName), "local SHA256SUMS")
	if err != nil {
		return selectedRelease{}, err
	}
	assetName := targetArtifact(manifest.Version)
	var selected ReleaseAsset
	count := 0
	for _, asset := range manifest.Assets {
		if asset.Name == assetName {
			selected = asset
			count++
		}
	}
	if count != 1 {
		return selectedRelease{}, fmt.Errorf("local release manifest must contain exactly one %s", assetName)
	}
	digest, err := checksumForAsset(checksums, assetName)
	if err != nil {
		return selectedRelease{}, err
	}
	if digest != selected.SHA256 {
		return selectedRelease{}, errors.New("release manifest and SHA256SUMS disagree")
	}
	if err := validateChecksumInventory(manifest, manifestBody, checksums); err != nil {
		return selectedRelease{}, err
	}
	if err := validateLocalInventory(absolute, manifest, checksums); err != nil {
		return selectedRelease{}, err
	}
	return selectedRelease{manifest: manifest, asset: selected, localDir: absolute, channel: expectedChannel}, nil
}

func loadReleaseManifest(path string) (ReleaseManifest, []byte, error) {
	body, err := readReleaseMetadata(path, "release manifest")
	if err != nil {
		return ReleaseManifest{}, nil, err
	}
	manifest, err := decodeReleaseManifest(body)
	if err != nil {
		return ReleaseManifest{}, nil, err
	}
	return manifest, body, nil
}

func readReleaseMetadata(path, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a real regular file", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxReleaseMetadataBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read %s: %w", label, errors.Join(readErr, closeErr))
	}
	if len(body) > maxReleaseMetadataBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", label, maxReleaseMetadataBytes)
	}
	return body, nil
}

func decodeReleaseManifest(body []byte) (ReleaseManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest ReleaseManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ReleaseManifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return ReleaseManifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateReleaseManifest(manifest); err != nil {
		return ReleaseManifest{}, err
	}
	return manifest, nil
}

func validateOnlineInventory(release githubRelease, manifest ReleaseManifest) error {
	expected := map[string]int64{
		releaseManifestName:  -1,
		releaseChecksumsName: -1,
	}
	for _, asset := range manifest.Assets {
		expected[asset.Name] = asset.Size
	}
	if len(release.Assets) != len(expected) {
		return errors.New("GitHub release inventory does not match the strict release manifest")
	}
	for _, asset := range release.Assets {
		expectedSize, ok := expected[asset.Name]
		if !ok {
			return fmt.Errorf("GitHub release contains unmanifested asset %q", asset.Name)
		}
		if expectedSize >= 0 && asset.Size != expectedSize {
			return fmt.Errorf("GitHub release asset size disagrees with manifest: %s", asset.Name)
		}
	}
	return nil
}

func releaseAssetByName(manifest ReleaseManifest, name string) (ReleaseAsset, bool) {
	for _, asset := range manifest.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return ReleaseAsset{}, false
}

func validateReleaseManifest(manifest ReleaseManifest) error {
	if manifest.FormatVersion != releaseManifestFormat || manifest.Repository != releaseRepository {
		return errors.New("unsupported release manifest identity")
	}
	version, err := versionFromTag(manifest.Tag)
	if err != nil || version != manifest.Version {
		return errors.New("release manifest tag and version disagree")
	}
	if len(manifest.Assets) == 0 || len(manifest.Assets) > maxReleaseAssets {
		return fmt.Errorf("release manifest must contain 1 to %d assets", maxReleaseAssets)
	}
	seen := map[string]bool{}
	previousName := ""
	for _, asset := range manifest.Assets {
		if filepath.Base(asset.Name) != asset.Name || !artifactNamePattern.MatchString(asset.Name) ||
			!sha256Pattern.MatchString(asset.SHA256) || asset.Size <= 0 {
			return fmt.Errorf("invalid release asset %q", asset.Name)
		}
		if seen[asset.Name] {
			return fmt.Errorf("duplicate release asset %q", asset.Name)
		}
		if previousName != "" && asset.Name <= previousName {
			return errors.New("release assets must be sorted by name")
		}
		seen[asset.Name] = true
		previousName = asset.Name
	}
	return nil
}

func validateLocalInventory(directory string, manifest ReleaseManifest, checksums []byte) error {
	expected := map[string]bool{releaseManifestName: true, releaseChecksumsName: true}
	for _, asset := range manifest.Assets {
		expected[asset.Name] = true
		path := filepath.Join(directory, asset.Name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("local release asset is missing or irregular: %s", asset.Name)
		}
		if info.Size() != asset.Size {
			return fmt.Errorf("local release asset size mismatch: %s", asset.Name)
		}
		checksum, err := checksumForAsset(checksums, asset.Name)
		if err != nil || checksum != asset.SHA256 {
			return fmt.Errorf("local checksum identity failed for %s", asset.Name)
		}
		actual, err := fileSHA256(path)
		if err != nil || actual != asset.SHA256 {
			return fmt.Errorf("local release asset checksum mismatch: %s", asset.Name)
		}
	}
	entries, err := boundedio.ReadDirNoSymlink(directory, maxReleaseDirEntries)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("local release entry must be a real regular file: %s", entry.Name())
		}
		if !expected[entry.Name()] && !strings.HasSuffix(entry.Name(), ".sigstore.jsonl") &&
			entry.Name() != "trusted_root.jsonl" {
			return fmt.Errorf("unexpected local release entry %q", entry.Name())
		}
	}
	return nil
}

func validateChecksumInventory(manifest ReleaseManifest, manifestBody, checksums []byte) error {
	expected := make(map[string]string, len(manifest.Assets)+1)
	for _, asset := range manifest.Assets {
		expected[asset.Name] = asset.SHA256
	}
	manifestDigest := sha256.Sum256(manifestBody)
	expected[releaseManifestName] = hex.EncodeToString(manifestDigest[:])
	actual, err := parseChecksumManifest(checksums)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return errors.New("SHA256SUMS inventory does not match the strict release manifest")
	}
	for name, digest := range expected {
		if actual[name] != digest {
			return fmt.Errorf("SHA256SUMS disagrees with release manifest for %s", name)
		}
	}
	return nil
}

func parseChecksumManifest(body []byte) (map[string]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), maxReleaseMetadataBytes)
	out := map[string]string{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, errors.New("malformed SHA256SUMS entry")
		}
		digest := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name || !artifactNamePattern.MatchString(name) ||
			!sha256Pattern.MatchString(digest) {
			return nil, fmt.Errorf("invalid SHA256SUMS entry for %q", name)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS entry for %s", name)
		}
		out[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func materializeCandidate(ctx context.Context, release selectedRelease, destination string) error {
	var body []byte
	var err error
	if release.localDir != "" {
		source := filepath.Join(release.localDir, release.asset.Name)
		info, statErr := os.Lstat(source)
		if statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("local candidate is missing or irregular: %s", source)
		}
		body, err = boundedio.ReadRegularFile(source, maxBinaryBytes)
	} else {
		body, err = downloadBounded(ctx, release.asset.URL, maxBinaryBytes)
	}
	if err != nil {
		return err
	}
	if int64(len(body)) != release.asset.Size {
		return errors.New("release candidate size mismatch")
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != release.asset.SHA256 {
		return errors.New("release candidate checksum mismatch")
	}
	if err := os.WriteFile(destination, body, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return fmt.Errorf("make release candidate executable: %w", err)
	}
	provenance, err := buildprovenance.InspectBinary(destination)
	if err != nil {
		return err
	}
	if provenance.Version != release.manifest.Version ||
		provenance.GOOS != runtime.GOOS || provenance.GOARCH != runtime.GOARCH {
		return errors.New("release candidate embedded identity does not match target")
	}
	return nil
}

func checksumForAsset(manifest []byte, assetName string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	scanner.Buffer(make([]byte, 64<<10), maxReleaseMetadataBytes)
	digest := ""
	matches := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return "", errors.New("malformed SHA256SUMS entry")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		if !sha256Pattern.MatchString(strings.ToLower(fields[0])) {
			return "", fmt.Errorf("invalid checksum for %s", assetName)
		}
		digest = strings.ToLower(fields[0])
		matches++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if matches != 1 {
		return "", fmt.Errorf("SHA256SUMS must contain exactly one %s entry", assetName)
	}
	return digest, nil
}

func validateReleaseClass(version string, draft bool, prerelease bool, channel Channel) error {
	parsed, err := parseSemanticVersion(version)
	if err != nil {
		return err
	}
	if draft {
		return errors.New("draft releases cannot be selected")
	}
	actualPrerelease := len(parsed.prerelease) > 0
	if actualPrerelease != prerelease {
		return errors.New("release prerelease flag and semantic version disagree")
	}
	if channel == ChannelStable && actualPrerelease {
		return errors.New("stable channel cannot select a prerelease")
	}
	if channel == ChannelPreview && !actualPrerelease {
		return errors.New("preview channel requires a prerelease")
	}
	return nil
}

func versionFromTag(tag string) (string, error) {
	if !strings.HasPrefix(tag, "reconc-v") {
		return "", fmt.Errorf("unexpected release tag %q", tag)
	}
	version := strings.TrimPrefix(tag, "reconc-v")
	if _, err := parseSemanticVersion(version); err != nil {
		return "", err
	}
	return version, nil
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	match := semanticVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 5 {
		return semanticVersion{}, fmt.Errorf("version %q is not supported semantic versioning", value)
	}
	parts := make([]uint64, 3)
	for index := range parts {
		number, err := strconv.ParseUint(match[index+1], 10, 64)
		if err != nil {
			return semanticVersion{}, fmt.Errorf("version component overflows: %q", value)
		}
		parts[index] = number
	}
	prerelease := []string{}
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
		for _, identifier := range prerelease {
			if identifier == "" || allDigits(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, fmt.Errorf("invalid prerelease identifier in %q", value)
			}
		}
	}
	return semanticVersion{
		major: parts[0], minor: parts[1], patch: parts[2],
		prerelease: prerelease, original: strings.TrimSpace(value),
	}, nil
}

func compareSemanticVersions(left semanticVersion, right semanticVersion) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		comparison := comparePrereleaseIdentifier(left.prerelease[index], right.prerelease[index])
		if comparison != 0 {
			return comparison
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func comparePrereleaseIdentifier(left string, right string) int {
	leftNumeric := allDigits(left)
	rightNumeric := allDigits(right)
	if leftNumeric && rightNumeric {
		if len(left) < len(right) {
			return -1
		}
		if len(left) > len(right) {
			return 1
		}
		return strings.Compare(left, right)
	}
	if leftNumeric {
		return -1
	}
	if rightNumeric {
		return 1
	}
	return strings.Compare(left, right)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func sortedReleaseAssets(assets []ReleaseAsset) []ReleaseAsset {
	out := append([]ReleaseAsset(nil), assets...)
	sort.Slice(out, func(left, right int) bool {
		return out[left].Name < out[right].Name
	})
	return out
}
