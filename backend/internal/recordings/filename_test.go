package recordings

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const nativeExample = "20260905_081609Greenwich_Fairfield_T-NEW_GFD1__TO_57201_FROM_578060.mp3"

func TestNativeFilename(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	m, reason := ParseFilename(nativeExample, zone)
	if reason != "" {
		t.Fatal(reason)
	}
	if m.RecordedAt.UTC().Format(time.RFC3339) != "2026-09-05T12:16:09Z" ||
		m.SystemSiteLabel != "Greenwich_Fairfield" || m.AliasChannelLabel != "T-NEW_GFD1" ||
		m.TGID != 57201 || m.RID != 578060 || m.ChannelLabel != "CH1A" ||
		m.Extension != ".mp3" || m.OriginalFilename != nativeExample || m.Timezone != "America/New_York" {
		t.Fatalf("unexpected parsed metadata: %+v", m)
	}
}

func TestTalkgroupsAndExtensions(t *testing.T) {
	for i, channel := range []string{"CH1A", "CH2B", "CH3B", "CH4C"} {
		for _, extension := range []string{"mp3", "wav", "MP3", "WAV"} {
			name := fmt.Sprintf("20260905_081609_Greenwich_Fairfield_T-NEW_GFD1__TO_%d_FROM_577811.%s", 57201+i, extension)
			m, reason := ParseFilename(name, time.UTC)
			if reason != "" || m.ChannelLabel != channel || m.RID != 577811 || m.Extension != "."+strings.ToLower(extension) {
				t.Fatalf("%s: %+v, %s", name, m, reason)
			}
		}
	}
}

func TestIgnoredFilenames(t *testing.T) {
	zone, _ := time.LoadLocation("America/New_York")
	for _, tc := range []struct{ name, reason string }{
		{"bad.mp3", "malformed_filename"},
		{strings.Replace(nativeExample, "57201", "12345", 1), "unsupported_talkgroup"},
		{strings.Replace(nativeExample, "578060", "0", 1), "invalid_radio_id"},
		{strings.Replace(nativeExample, "578060", "999999999999999999999999", 1), "invalid_radio_id"},
		{strings.Replace(nativeExample, "20260905", "20260230", 1), "invalid_timestamp"},
		{strings.Replace(nativeExample, "20260905_081609", "20260308_023000", 1), "invalid_timestamp"},
		{strings.Replace(nativeExample, "20260905_081609", "20261101_013000", 1), "ambiguous_timestamp"},
		{strings.Replace(nativeExample, "Greenwich_Fairfield_", "", 1), "malformed_labels"},
		{strings.Replace(nativeExample, ".mp3", ".txt", 1), "unsupported_extension"},
		{"TEST_" + nativeExample, "excluded_prefix"},
		{"REPLAY_" + nativeExample, "excluded_prefix"},
		{"demo_" + nativeExample, "excluded_prefix"},
		{"tEsT_" + nativeExample, "excluded_prefix"},
		{"../" + nativeExample, "malformed_filename"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := ParseFilename(tc.name, zone); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestSourceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), nativeExample)
	a := SourceIdentity(path)
	if len(a) != 64 || a != SourceIdentity(path) || a == SourceIdentity(path+"other") {
		t.Fatal("unstable source identity")
	}
}
