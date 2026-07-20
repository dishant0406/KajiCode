package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ChannelOptions struct {
	RootDir         string
	ReleaseDir      string
	OutputPath      string
	Version         string
	Repository      string
	BaseURL         string
	MinKajiVersion  string
	MaxKajiVersion  string
	ProtocolVersion int
	AllowPartial    bool
}

type Channel struct {
	SchemaVersion int            `json:"schemaVersion"`
	GeneratedAt   string         `json:"generatedAt"`
	Latest        string         `json:"latest"`
	Entries       []ChannelEntry `json:"entries"`
}

type ChannelEntry struct {
	Version         string                  `json:"version"`
	ProtocolVersion int                     `json:"protocolVersion"`
	MinKajiVersion  string                  `json:"minKajiVersion"`
	MaxKajiVersion  *string                 `json:"maxKajiVersion"`
	Assets          map[string]ChannelAsset `json:"assets"`
}

type ChannelAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ChannelResult struct {
	OutputPath string
	Channel    Channel
}

type channelTarget struct {
	Key    string
	GOOS   string
	GOARCH string
}

var channelTargets = []channelTarget{
	{Key: "linux-x64", GOOS: "linux", GOARCH: "amd64"},
	{Key: "linux-arm64", GOOS: "linux", GOARCH: "arm64"},
	{Key: "macos-x64", GOOS: "darwin", GOARCH: "amd64"},
	{Key: "macos-arm64", GOOS: "darwin", GOARCH: "arm64"},
	{Key: "windows-x64", GOOS: "windows", GOARCH: "amd64"},
}

func GenerateKajiChannel(options ChannelOptions) (ChannelResult, error) {
	rootDir, err := resolveRootDir(options.RootDir)
	if err != nil {
		return ChannelResult{}, err
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version, err = PackageVersion(rootDir)
		if err != nil {
			return ChannelResult{}, err
		}
	}
	releaseDir := strings.TrimSpace(options.ReleaseDir)
	if releaseDir == "" {
		releaseDir = filepath.Join(rootDir, "dist", "release")
	} else if !filepath.IsAbs(releaseDir) {
		releaseDir = filepath.Join(rootDir, releaseDir)
	}
	outputPath := strings.TrimSpace(options.OutputPath)
	if outputPath == "" {
		outputPath = filepath.Join(rootDir, "dist", "kaji-channel.json")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(rootDir, outputPath)
	}
	protocolVersion := options.ProtocolVersion
	if protocolVersion == 0 {
		protocolVersion = 1
	}
	minKajiVersion := strings.TrimSpace(options.MinKajiVersion)
	if minKajiVersion == "" {
		minKajiVersion = "0.4.1"
	}
	maxKajiVersion := strings.TrimSpace(options.MaxKajiVersion)
	repository := strings.TrimSpace(options.Repository)
	if repository == "" {
		repository = "dishant0406/KajiCode"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://github.com"
	}
	assets, err := channelAssets(releaseDir, version, baseURL, repository, options.AllowPartial)
	if err != nil {
		return ChannelResult{}, err
	}
	entry := ChannelEntry{
		Version:         version,
		ProtocolVersion: protocolVersion,
		MinKajiVersion:  minKajiVersion,
		Assets:          assets,
	}
	if maxKajiVersion != "" {
		entry.MaxKajiVersion = &maxKajiVersion
	}
	channel := Channel{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Latest:        version,
		Entries:       []ChannelEntry{entry},
	}
	data, err := json.MarshalIndent(channel, "", "  ")
	if err != nil {
		return ChannelResult{}, err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return ChannelResult{}, err
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return ChannelResult{}, err
	}
	return ChannelResult{OutputPath: outputPath, Channel: channel}, nil
}

func channelAssets(releaseDir string, version string, baseURL string, repository string, allowPartial bool) (map[string]ChannelAsset, error) {
	assets := map[string]ChannelAsset{}
	missing := []string{}
	for _, target := range channelTargets {
		archiveName, err := ReleaseArchiveName(version, target.GOOS, target.GOARCH)
		if err != nil {
			return nil, err
		}
		archivePath := filepath.Join(releaseDir, archiveName)
		checksumPath := archivePath + ".sha256"
		info, statErr := os.Stat(archivePath)
		if statErr != nil {
			missing = append(missing, archiveName)
			continue
		}
		verified, err := VerifySHA256Checksum(checksumPath)
		if err != nil {
			return nil, err
		}
		if verified.ArchiveName != archiveName {
			return nil, fmt.Errorf("checksum file %s references %s, expected %s", filepath.Base(checksumPath), verified.ArchiveName, archiveName)
		}
		assets[target.Key] = ChannelAsset{
			URL:    fmt.Sprintf("%s/%s/releases/download/v%s/%s", baseURL, repository, version, archiveName),
			SHA256: verified.ActualChecksum,
			Size:   info.Size(),
		}
	}
	if len(missing) > 0 && !allowPartial {
		sort.Strings(missing)
		return nil, fmt.Errorf("release dir is missing channel asset archive(s): %s", strings.Join(missing, ", "))
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("release dir contains no channel assets for version %s", version)
	}
	return assets, nil
}
