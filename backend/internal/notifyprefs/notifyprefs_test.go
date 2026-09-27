package notifyprefs

import (
	"math"
	"testing"
	"time"

	"greenwich-fire-responder/backend/internal/identity"
)

func durPtr(d time.Duration) *time.Duration { return &d }
func strPtr(s string) *string               { return &s }
func floatPtr(f float64) *float64           { return &f }

func TestChannelValid(t *testing.T) {
	valid := []Channel{ChannelCH1A, ChannelCH2B, ChannelCH3B, ChannelCH4C}
	for _, c := range valid {
		if !c.Valid() {
			t.Fatalf("expected %q to be valid", c)
		}
	}
	invalid := []Channel{"", "CH5D", "ch1a", "CH1A "}
	for _, c := range invalid {
		if c.Valid() {
			t.Fatalf("expected %q to be invalid", c)
		}
	}
}

func minimalPreferences() Preferences {
	return Preferences{UserID: 1}
}

func TestValidateRejectsNonPositiveUserID(t *testing.T) {
	for _, id := range []identity.UserID{0, -1} {
		p := minimalPreferences()
		p.UserID = id
		if err := p.Validate(); err != ErrInvalidUserID {
			t.Fatalf("expected ErrInvalidUserID for user id %v, got %v", id, err)
		}
	}
}

func TestValidateMinimalPreferencesSucceeds(t *testing.T) {
	if err := minimalPreferences().Validate(); err != nil {
		t.Fatalf("expected a minimal, otherwise-empty Preferences to validate, got %v", err)
	}
}

func TestValidateChannels(t *testing.T) {
	cases := []struct {
		name     string
		channels []Channel
		want     error
	}{
		{"all four valid, no duplicates", []Channel{ChannelCH1A, ChannelCH2B, ChannelCH3B, ChannelCH4C}, nil},
		{"single valid", []Channel{ChannelCH2B}, nil},
		{"empty", []Channel{}, nil},
		{"nil", nil, nil},
		{"invalid value", []Channel{"CH5D"}, ErrInvalidChannel},
		{"duplicate", []Channel{ChannelCH1A, ChannelCH1A}, ErrDuplicateChannel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.Channels = c.channels
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func repeatIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "id-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	return out
}

func TestValidateIDLists(t *testing.T) {
	valid200 := repeatIDs(200)
	cases := []struct {
		name string
		tone []string
		kw   []string
		want error
	}{
		{"empty", nil, nil, nil},
		{"valid ids", []string{"engine-1", "TG_57201"}, []string{"structure-fire"}, nil},
		{"exactly 200 tone ids ok", valid200, nil, nil},
		{"201 tone ids too many", append(append([]string{}, valid200...), "one-more"), nil, ErrTooManyIDs},
		{"201 keyword ids too many", nil, append(append([]string{}, valid200...), "one-more"), ErrTooManyIDs},
		{"invalid character", []string{"bad id!"}, nil, ErrInvalidID},
		{"leading underscore invalid", []string{"_bad"}, nil, ErrInvalidID},
		{"empty string invalid", []string{""}, nil, ErrInvalidID},
		{"too long invalid", []string{string(make([]byte, 129))}, nil, ErrInvalidID},
		{"duplicate tone id", []string{"dup", "dup"}, nil, ErrDuplicateID},
		{"duplicate keyword id", nil, []string{"dup", "dup"}, ErrDuplicateID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.ToneSetIDs = c.tone
			p.KeywordListIDs = c.kw
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestValidateMinConfidence(t *testing.T) {
	cases := []struct {
		name string
		c    *float64
		want error
	}{
		{"nil", nil, nil},
		{"zero boundary", floatPtr(0), nil},
		{"one boundary", floatPtr(1), nil},
		{"mid value", floatPtr(0.5), nil},
		{"below zero", floatPtr(-0.001), ErrInvalidMinConfidence},
		{"above one", floatPtr(1.001), ErrInvalidMinConfidence},
		{"NaN", floatPtr(math.NaN()), ErrInvalidMinConfidence},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.MinConfidence = c.c
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestValidateQuietHoursPairing(t *testing.T) {
	cases := []struct {
		name  string
		start *time.Duration
		end   *time.Duration
		tz    *string
		want  error
	}{
		{"both nil, no timezone", nil, nil, nil, nil},
		{"both nil, timezone still validated and valid", nil, nil, strPtr("America/New_York"), nil},
		{"both nil, timezone still validated and invalid", nil, nil, strPtr("Not/AZone"), ErrInvalidTimezone},
		{"only start set", durPtr(time.Hour), nil, strPtr("UTC"), ErrQuietHoursUnpaired},
		{"only end set", nil, durPtr(time.Hour), strPtr("UTC"), ErrQuietHoursUnpaired},
		{"both set, valid, with timezone", durPtr(22 * time.Hour), durPtr(6 * time.Hour), strPtr("America/New_York"), nil},
		{"both set, no timezone", durPtr(22 * time.Hour), durPtr(6 * time.Hour), nil, ErrQuietHoursTimezoneRequired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.QuietHoursStart = c.start
			p.QuietHoursEnd = c.end
			p.QuietHoursTimezone = c.tz
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestValidateQuietHoursRange(t *testing.T) {
	cases := []struct {
		name  string
		start time.Duration
		end   time.Duration
		want  error
	}{
		{"zero start ok", 0, time.Hour, nil},
		{"just under 24h end ok", time.Hour, 24*time.Hour - time.Nanosecond, nil},
		{"negative start invalid", -time.Minute, time.Hour, ErrInvalidQuietHoursTime},
		{"negative end invalid", time.Hour, -time.Minute, ErrInvalidQuietHoursTime},
		{"start at exactly 24h invalid", 24 * time.Hour, time.Hour, ErrInvalidQuietHoursTime},
		{"end at exactly 24h invalid", time.Hour, 24 * time.Hour, ErrInvalidQuietHoursTime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.QuietHoursStart = durPtr(c.start)
			p.QuietHoursEnd = durPtr(c.end)
			p.QuietHoursTimezone = strPtr("UTC")
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}

func TestValidateTimezone(t *testing.T) {
	cases := []struct {
		name string
		tz   string
		want error
	}{
		{"UTC", "UTC", nil},
		{"IANA zone", "America/New_York", nil},
		{"another IANA zone", "Europe/London", nil},
		{"Local rejected", "Local", ErrInvalidTimezone},
		{"empty rejected", "", ErrInvalidTimezone},
		{"garbage rejected", "Not/AZone", ErrInvalidTimezone},
		{"too long rejected", string(make([]byte, 65)), ErrInvalidTimezone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := minimalPreferences()
			p.QuietHoursStart = durPtr(time.Hour)
			p.QuietHoursEnd = durPtr(2 * time.Hour)
			p.QuietHoursTimezone = strPtr(c.tz)
			if err := p.Validate(); err != c.want {
				t.Fatalf("expected %v, got %v", c.want, err)
			}
		})
	}
}
