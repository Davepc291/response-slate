package config

import (
	"greenwich-fire-responder/backend/internal/operations"
	"strings"
	"testing"
)

func TestOperationsConfiguration(t *testing.T) {
	for _, key := range []string{"QUEUE_DEPTH", "OLDEST_AGE", "REQUEST_DURATION", "CONSECUTIVE_FAILURES"} {
		t.Setenv("GFR_MONITOR_WARN_"+key, "")
	}
	o := operations.DefaultOptions()
	if loadOperations(&o) != nil || o != operations.DefaultOptions() {
		t.Fatal("defaults")
	}
	for key, value := range map[string]string{"QUEUE_DEPTH": "0", "OLDEST_AGE": "-1s", "REQUEST_DURATION": "secret", "CONSECUTIVE_FAILURES": "101"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("GFR_MONITOR_WARN_"+key, value)
			o := operations.DefaultOptions()
			err := loadOperations(&o)
			if err == nil || strings.Contains(err.Error(), value) {
				t.Fatal("unsafe validation", err)
			}
		})
	}
	t.Setenv("GFR_MONITOR_WARN_QUEUE_DEPTH", "20")
	t.Setenv("GFR_MONITOR_WARN_OLDEST_AGE", "1m")
	t.Setenv("GFR_MONITOR_WARN_REQUEST_DURATION", "2s")
	t.Setenv("GFR_MONITOR_WARN_CONSECUTIVE_FAILURES", "2")
	if loadOperations(&o) != nil || o.QueueDepth != 20 || o.ConsecutiveFailures != 2 {
		t.Fatal(o)
	}
}
