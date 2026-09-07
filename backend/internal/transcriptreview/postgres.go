package transcriptreview

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}
type Postgres struct{ DB Querier }

func Open(ctx context.Context, connection string) (*Postgres, func(), error) {
	u, e := url.Parse(connection)
	if e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return nil, nil, ErrUnavailable
	}
	cfg, e := pgxpool.ParseConfig(connection)
	if e != nil {
		return nil, nil, ErrUnavailable
	}
	// A URL parameter must not redirect the reviewed loopback target or create
	// secondary/fallback connections elsewhere.
	if cfg.ConnConfig.Host != u.Hostname() || len(cfg.ConnConfig.Fallbacks) > 0 {
		// pgx may create same-host SSL fallback entries; remove them, do not follow.
		if cfg.ConnConfig.Host != u.Hostname() {
			return nil, nil, ErrUnavailable
		}
		cfg.ConnConfig.Fallbacks = nil
	}
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.MaxConns = 1
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, nil, ErrUnavailable
	}
	if p.Ping(ctx) != nil {
		p.Close()
		return nil, nil, ErrUnavailable
	}
	return &Postgres{p}, p.Close, nil
}
func safeDB(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIneligible
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23503" || pg.Code == "23505" || pg.Code == "22001") {
		return ErrIneligible
	}
	return ErrUnavailable
}

const candidateColumns = `c.transmission_id,c.transcription_attempt_id,coalesce(c.channel_label,''),coalesce(c.tgid,0),c.duration_ms,c.model,coalesce(l.verdict,'unreviewed')`

func (s *Postgres) Queue(ctx context.Context, limit int) ([]Candidate, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInput
	}
	rows, e := s.DB.Query(ctx, `SELECT `+candidateColumns+` FROM transcript_review_candidates c LEFT JOIN transcript_review_latest l USING(transmission_id) WHERE l.verdict IS DISTINCT FROM 'accepted' ORDER BY c.transmission_id LIMIT $1`, limit)
	if e != nil {
		return nil, safeDB(e)
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		if e = rows.Scan(&c.TransmissionID, &c.AttemptID, &c.Channel, &c.TGID, &c.DurationMS, &c.Model, &c.Verdict); e != nil {
			return nil, safeDB(e)
		}
		out = append(out, c)
	}
	return out, safeDB(rows.Err())
}
func (s *Postgres) Show(ctx context.Context, id int64) (Detail, error) {
	var d Detail
	if id < 1 {
		return d, ErrInput
	}
	e := s.DB.QueryRow(ctx, `SELECT `+candidateColumns+`,c.audio_fingerprint,coalesce(c.source_path,''),c.raw_model_transcript FROM transcript_review_candidates c LEFT JOIN transcript_review_latest l USING(transmission_id) WHERE c.transmission_id=$1`, id).Scan(&d.TransmissionID, &d.AttemptID, &d.Channel, &d.TGID, &d.DurationMS, &d.Model, &d.Verdict, &d.Fingerprint, &d.SourcePath, &d.Raw)
	return d, safeDB(e)
}
func (s *Postgres) Submit(ctx context.Context, in Submission) (Review, error) {
	var r Review
	if e := in.Validate(); e != nil {
		return r, e
	}
	e := s.DB.QueryRow(ctx, `INSERT INTO transcript_reviews(transmission_id,transcription_attempt_id,verdict,reviewer_label,reference_transcript,exclusion_reason,notes) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,transcription_attempt_id,verdict,reviewer_label,coalesce(reference_transcript,''),coalesce(exclusion_reason,''),coalesce(notes,''),reviewed_at`, in.TransmissionID, in.AttemptID, in.Verdict, in.Reviewer, in.Reference, in.Reason, in.Notes).Scan(&r.ID, &r.AttemptID, &r.Verdict, &r.Reviewer, &r.Reference, &r.Reason, &r.Notes, &r.ReviewedAt)
	return r, safeDB(e)
}
func (s *Postgres) History(ctx context.Context, id int64, limit int) ([]Review, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInput
	}
	if _, e := s.Show(ctx, id); e != nil {
		return nil, e
	}
	rows, e := s.DB.Query(ctx, `SELECT id,transcription_attempt_id,verdict,reviewer_label,coalesce(reference_transcript,''),coalesce(exclusion_reason,''),coalesce(notes,''),reviewed_at FROM transcript_reviews WHERE transmission_id=$1 ORDER BY id DESC LIMIT $2`, id, limit)
	if e != nil {
		return nil, safeDB(e)
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var r Review
		if e = rows.Scan(&r.ID, &r.AttemptID, &r.Verdict, &r.Reviewer, &r.Reference, &r.Reason, &r.Notes, &r.ReviewedAt); e != nil {
			return nil, safeDB(e)
		}
		out = append(out, r)
	}
	return out, safeDB(rows.Err())
}
func (s *Postgres) Stats(ctx context.Context) (Stats, error) {
	var r Stats
	e := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE l.verdict='accepted'),count(*) FILTER(WHERE l.verdict='excluded'),count(*) FILTER(WHERE l.verdict='needs_followup'),count(*) FILTER(WHERE l.id IS NULL),count(*) FILTER(WHERE l.verdict='accepted' AND d.split='train'),count(*) FILTER(WHERE l.verdict='accepted' AND d.split='validation'),count(*) FILTER(WHERE l.verdict='accepted' AND d.split='test') FROM transcript_review_candidates c LEFT JOIN transcript_review_latest l USING(transmission_id) LEFT JOIN transcript_dataset_items d USING(transmission_id)`).Scan(&r.Accepted, &r.Excluded, &r.Followup, &r.Unreviewed, &r.Train, &r.Validation, &r.Test)
	return r, safeDB(e)
}
func (s *Postgres) Export(ctx context.Context, limit int, emit func(ExportRecord) error) error {
	if limit < 1 || limit > MaxExportRecords {
		return ErrInput
	}
	rows, e := s.DB.Query(ctx, `SELECT 'gfr-audio-v1:'||md5(d.audio_fingerprint),d.audio_fingerprint,l.transcription_attempt_id,coalesce(c.channel_label,''),coalesce(c.tgid,0),c.duration_ms,l.raw_model_transcript,l.reference_transcript,l.model,d.split,l.reviewed_at FROM transcript_review_latest l JOIN transcript_dataset_items d USING(transmission_id) JOIN transcript_review_candidates c ON c.transmission_id=l.transmission_id AND c.transcription_attempt_id=l.transcription_attempt_id WHERE l.verdict='accepted' ORDER BY d.audio_fingerprint LIMIT $1`, limit)
	if e != nil {
		return safeDB(e)
	}
	defer rows.Close()
	for rows.Next() {
		var r ExportRecord
		if e = rows.Scan(&r.DatasetID, &r.Fingerprint, &r.AttemptID, &r.Channel, &r.TGID, &r.DurationMS, &r.Raw, &r.Reference, &r.Model, &r.Split, &r.ReviewedAt); e != nil {
			return safeDB(e)
		}
		r.ReviewedAt = r.ReviewedAt.UTC()
		if e = emit(r); e != nil {
			return e
		}
	}
	return safeDB(rows.Err())
}
