package transcriptreview

// All simulated submissions live inside one rolled-back transaction. They are
// explicitly synthetic, not a claim that any human listened or reviewed audio.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestControlledReviewCLI(t *testing.T) {
	if os.Getenv("GFR_REVIEW_LIVE_TEST") != "true" {
		t.Skip("explicit local rollback-only database opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, closeDB, e := Open(ctx, os.Getenv("GFR_DATABASE_URL"))
	if e != nil {
		t.Fatal("local review database unavailable")
	}
	defer closeDB()
	pool := db.DB.(*pgxpool.Pool)
	type baseline struct {
		transmissions, attempts, decisions, items, reviews int
		transmissionHash, attemptHash                      string
	}
	snapshot := func() baseline {
		t.Helper()
		var b baseline
		e := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM radio_transmissions),(SELECT count(*) FROM transcription_attempts),(SELECT count(*) FROM unit_status_decisions),(SELECT count(*) FROM transcript_dataset_items),(SELECT count(*) FROM transcript_reviews),(SELECT md5(string_agg(to_jsonb(t)::text,'' ORDER BY id)) FROM radio_transmissions t),(SELECT md5(string_agg(to_jsonb(t)::text,'' ORDER BY id)) FROM transcription_attempts t)`).Scan(&b.transmissions, &b.attempts, &b.decisions, &b.items, &b.reviews, &b.transmissionHash, &b.attemptHash)
		if e != nil {
			t.Fatal("baseline query failed")
		}
		return b
	}
	before := snapshot()
	if before.transmissions != 21 || before.attempts != 23 || before.decisions != 0 || before.items != 0 || before.reviews != 0 {
		t.Fatal("unexpected local verification baseline")
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal("test transaction failed")
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			if tx.Rollback(cleanup) != nil {
				t.Error("rollback failed")
			}
		}
	}()
	store := &Postgres{tx}
	nonce := fmt.Sprint(time.Now().UnixNano())
	sum := sha256.Sum256([]byte("SYNTHETIC REVIEW TEST " + nonce))
	src := hex.EncodeToString(sum[:])
	fp := "pcm-s16le-16000-mono-v1:sha256:" + src
	var id int64
	e = tx.QueryRow(ctx, `INSERT INTO radio_transmissions(source_identity,source_path,source_filename,source_recorder,tgid,rid,recorded_at,system_site_label,alias_channel_label,channel_label,extension,recording_timezone,source_size_bytes,source_modified_at) VALUES($1,'synthetic-private/fixture.mp3','fixture.mp3','SDRTrunk',57201,987654321,now(),'synthetic','synthetic','CH1A','.mp3','America/New_York',100,now()) RETURNING id`, src).Scan(&id)
	if e != nil {
		t.Fatal("synthetic transmission fixture failed")
	}
	if _, e = tx.Exec(ctx, `UPDATE radio_transmissions SET processing_status='processing' WHERE id=$1`, id); e != nil {
		t.Fatal("synthetic analysis claim failed")
	}
	var result string
	if tx.QueryRow(ctx, `SELECT complete_audio_analysis($1,$2,1000,0.1,0.5,'{}')`, src, fp).Scan(&result) != nil {
		t.Fatal("synthetic analysis failed")
	}
	var ok bool
	claim := src[:32]
	if tx.QueryRow(ctx, `SELECT claim_transcription($1,$2::uuid,3,90,'openai-compatible-http-v1','small.en','{"token":"SYNTHETIC_TOKEN_SECRET","provider_url":"http://synthetic.invalid","filename":"fixture.mp3"}')`, id, claim).Scan(&ok) != nil || !ok {
		t.Fatal("synthetic transcription claim failed")
	}
	raw := "  SYNTHETIC model evidence, not operational audio.\n"
	if tx.QueryRow(ctx, `SELECT finish_transcription($1,$2::uuid,$3,$3,NULL,false,5)`, id, claim, raw).Scan(&ok) != nil || !ok {
		t.Fatal("synthetic finish failed")
	}
	run := func(args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		if e := Run(ctx, args, &out, store, 10*time.Second); e != nil {
			t.Fatalf("CLI %s failed: %v", args[0], e)
		}
		return out.Bytes()
	}
	var queue []Candidate
	if json.Unmarshal(run("queue", "--limit", "1"), &queue) != nil || len(queue) != 1 {
		t.Fatal("queue bound failed")
	}
	if bytes.Contains(run("queue", "--limit", "200"), []byte("synthetic-private")) {
		t.Fatal("path leaked from queue")
	}
	idArg := strconv.FormatInt(id, 10)
	var detail Detail
	if json.Unmarshal(run("show", "--id", idArg), &detail) != nil || detail.Raw != raw || detail.SourcePath != "synthetic-private/fixture.mp3" {
		t.Fatal("show did not preserve selected evidence")
	}
	dir, e := os.MkdirTemp("", "gfr-review-cli-")
	if e != nil {
		t.Fatal("temporary directory failed")
	}
	textFile := filepath.Join(dir, "reference.txt")
	exportFile := filepath.Join(dir, "dataset.jsonl")
	secondFile := filepath.Join(dir, "again.jsonl")
	defer func() {
		for _, p := range []string{textFile, exportFile, secondFile} {
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				t.Error("temporary file cleanup failed")
			}
		}
		if e := os.Remove(dir); e != nil {
			t.Error("temporary directory cleanup failed")
		}
	}()
	reference := "SYNTHETIC fixture reference; not a genuine human review.\n"
	if os.WriteFile(textFile, []byte(reference), 0600) != nil {
		t.Fatal("reference fixture failed")
	}
	// This acknowledgment exercises CLI validation only. The transaction never commits.
	submit := func(verdict string) {
		args := []string{"submit", "--id", idArg, "--attempt", strconv.FormatInt(detail.AttemptID, 10), "--reviewer", "SYNTHETIC-TEST-NOT-HUMAN", "--verdict", verdict, "--confirm-human", "--notes", "SYNTHETIC_REVIEWER_SECRET"}
		if verdict == "accepted" {
			args = append(args, "--text-file", textFile)
		}
		if verdict == "excluded" {
			args = append(args, "--reason", "SYNTHETIC exclusion")
		}
		run(args...)
	}
	submit("accepted")
	run("export", "--output", exportFile, "--limit", "100")
	exported, e := os.ReadFile(exportFile)
	if e != nil {
		t.Fatal("export missing")
	}
	var record ExportRecord
	if json.Unmarshal(exported, &record) != nil || record.Reference != reference || record.Raw != raw || record.Fingerprint != fp {
		t.Fatal("export evidence differs")
	}
	for _, secret := range []string{"synthetic-private", "fixture.mp3", "987654321", "SYNTHETIC_TOKEN_SECRET", "http://synthetic.invalid", "SYNTHETIC_REVIEWER_SECRET", "SYNTHETIC-TEST-NOT-HUMAN"} {
		if bytes.Contains(exported, []byte(secret)) {
			t.Fatal("export leaked excluded metadata")
		}
	}
	run("export", "--output", secondFile, "--limit", "100")
	again, _ := os.ReadFile(secondFile)
	if !bytes.Equal(exported, again) {
		t.Fatal("exports not stable")
	}
	var stats Stats
	if json.Unmarshal(run("stats"), &stats) != nil || stats.Accepted != 1 {
		t.Fatal("accepted stats")
	}
	submit("needs_followup")
	run("export", "--output", exportFile, "--overwrite")
	empty, _ := os.ReadFile(exportFile)
	if len(empty) != 0 {
		t.Fatal("follow-up remained accepted")
	}
	if json.Unmarshal(run("stats"), &stats) != nil || stats.Followup != 1 || stats.Accepted != 0 {
		t.Fatal("follow-up stats")
	}
	submit("excluded")
	if json.Unmarshal(run("stats"), &stats) != nil || stats.Excluded != 1 {
		t.Fatal("excluded stats")
	}
	submit("accepted")
	var history []Review
	if json.Unmarshal(run("history", "--id", idArg), &history) != nil || len(history) != 4 || history[0].ID <= history[1].ID {
		t.Fatal("append-only history or ordering")
	}
	run("export", "--output", exportFile, "--overwrite")
	final, _ := os.ReadFile(exportFile)
	var rer ExportRecord
	if json.Unmarshal(final, &rer) != nil || rer.Split != record.Split {
		t.Fatal("re-review changed split")
	}
	var unchanged string
	if tx.QueryRow(ctx, `SELECT raw_text FROM transcription_attempts WHERE id=$1`, detail.AttemptID).Scan(&unchanged) != nil || unchanged != raw {
		t.Fatal("raw attempt overwritten")
	}
	if strings.Contains(string(run("stats")), "synthetic-private") {
		t.Fatal("stats path leak")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("synthetic review rollback failed")
	}
	rolledBack = true
	after := snapshot()
	if before != after {
		t.Fatal("legitimate rows or dataset baseline changed")
	}
	t.Log("SYNTHETIC ROLLBACK VERIFICATION: queue bounded; show exact; accepted/follow-up/excluded submissions; history=4; stable accepted-only JSONL; privacy checks passed")
	t.Logf("baseline restored: transmissions=%d attempts=%d decisions=%d dataset_items=%d reviews=%d; content digests unchanged", after.transmissions, after.attempts, after.decisions, after.items, after.reviews)
}
