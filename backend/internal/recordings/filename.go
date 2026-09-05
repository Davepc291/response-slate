// Package recordings ingests SDRTrunk filename metadata without reading audio.
package recordings

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA timezone support available on Windows.
)

var nativeName = regexp.MustCompile(`^(\d{8}_\d{6})_?(.+)__TO_(\d+)_FROM_(\d+)$`)

type Metadata struct {
	RecordedAt        time.Time
	SystemSiteLabel   string
	AliasChannelLabel string
	ChannelLabel      string
	TGID              int64
	RID               int64
	Extension         string
	OriginalFilename  string
	Timezone          string
}

// ParseFilename returns a fixed, safe ignore reason rather than echoing input.
// The supported local convention has system_site_channel labels; the channel
// portion can contain underscores. No unit identification is performed here.
func ParseFilename(name string, location *time.Location) (Metadata, string) {
	var m Metadata
	lower := strings.ToLower(name)
	for _, prefix := range []string{"test_", "replay_", "demo_"} {
		if strings.HasPrefix(lower, prefix) {
			return m, "excluded_prefix"
		}
	}
	if strings.ContainsAny(name, `/\`+"\x00") || location == nil {
		return m, "malformed_filename"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".mp3" && ext != ".wav" {
		return m, "unsupported_extension"
	}
	parts := nativeName.FindStringSubmatch(name[:len(name)-len(ext)])
	if parts == nil {
		return m, "malformed_filename"
	}
	labels := strings.SplitN(parts[2], "_", 3)
	if len(labels) != 3 {
		return m, "malformed_labels"
	}
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			return m, "malformed_labels"
		}
	}
	recorded, err := time.ParseInLocation("20060102_150405", parts[1], location)
	if err != nil || recorded.Format("20060102_150405") != parts[1] {
		return m, "invalid_timestamp"
	}
	// A repeated wall-clock hour cannot identify an instant without an offset.
	for _, delta := range []time.Duration{-time.Hour, time.Hour} {
		if recorded.Add(delta).Format("20060102_150405") == parts[1] {
			return m, "ambiguous_timestamp"
		}
	}
	tgid, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || tgid < 57201 || tgid > 57204 {
		return m, "unsupported_talkgroup"
	}
	rid, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil || rid <= 0 {
		return m, "invalid_radio_id"
	}
	channels := map[int64]string{57201: "CH1A", 57202: "CH2B", 57203: "CH3B", 57204: "CH4C"}
	return Metadata{RecordedAt: recorded, SystemSiteLabel: labels[0] + "_" + labels[1],
		AliasChannelLabel: labels[2], ChannelLabel: channels[tgid], TGID: tgid, RID: rid,
		Extension: ext, OriginalFilename: name, Timezone: location.String()}, ""
}

type Recording struct {
	Metadata
	SourceIdentity string
	SourcePath     string
	SizeBytes      int64
	ModifiedAt     time.Time
}

// SourceIdentity uses a versioned absolute path, not audio bytes or mutable stat
// measurements. Windows paths are case folded for the usual NTFS semantics.
func SourceIdentity(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte("sdrtrunk-path-v1\x00"+path)))
}
