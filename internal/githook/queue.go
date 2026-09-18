package githook

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrQueueEmpty = sql.ErrNoRows

type Job struct {
	DeliveryID string `json:"delivery_id"`
	SourceID   string `json:"source_id,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Repository string `json:"repository,omitempty"`
	RunID      int64  `json:"run_id"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status,omitempty"`
	Attempts   int    `json:"attempts"`
	Available  int64  `json:"available_at,omitempty"`
	Created    int64  `json:"created_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

type Queue struct{ db *sql.DB }

func OpenQueue(path string) (*Queue, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create queue directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	q := &Queue{db: db}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS jobs (delivery_id TEXT PRIMARY KEY, source_id TEXT NOT NULL DEFAULT 'default', target_id TEXT NOT NULL DEFAULT 'default', repository TEXT NOT NULL DEFAULT '', run_id INTEGER NOT NULL UNIQUE, head_sha TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0, available_at INTEGER NOT NULL, created_at INTEGER NOT NULL, last_error TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(status, available_at, run_id);
CREATE UNIQUE INDEX IF NOT EXISTS one_processing_job ON jobs(status) WHERE status='processing';
CREATE TABLE IF NOT EXISTS deployment_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), run_id INTEGER NOT NULL, head_sha TEXT NOT NULL, deployed_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS target_deployment_state (target_id TEXT PRIMARY KEY, run_id INTEGER NOT NULL, head_sha TEXT NOT NULL, deployed_at INTEGER NOT NULL);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize queue: %w", err)
	}
	for _, migration := range []string{
		`ALTER TABLE jobs ADD COLUMN source_id TEXT NOT NULL DEFAULT 'default'`,
		`ALTER TABLE jobs ADD COLUMN target_id TEXT NOT NULL DEFAULT 'default'`,
		`ALTER TABLE jobs ADD COLUMN repository TEXT NOT NULL DEFAULT ''`,
	} {
		if _, migrationErr := db.Exec(migration); migrationErr != nil && !strings.Contains(migrationErr.Error(), "duplicate column name") {
			db.Close()
			return nil, fmt.Errorf("migrate queue identity: %w", migrationErr)
		}
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO target_deployment_state(target_id,run_id,head_sha,deployed_at) SELECT 'default',run_id,head_sha,deployed_at FROM deployment_state WHERE singleton=1`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate deployment state: %w", err)
	}
	return q, nil
}

func (q *Queue) Close() error { return q.db.Close() }

func (q *Queue) Enqueue(ctx context.Context, deliveryID string, runID int64, headSHA string) (bool, error) {
	return q.EnqueueFor(ctx, deliveryID, "default", "default", "", runID, headSHA)
}

func (q *Queue) EnqueueFor(ctx context.Context, deliveryID, sourceID, targetID, repository string, runID int64, headSHA string) (bool, error) {
	if sourceID == "" || targetID == "" || repository == "" && sourceID != "default" {
		return false, fmt.Errorf("source, target, and repository identities are required")
	}
	now := time.Now().Unix()
	r, err := q.db.ExecContext(ctx, `INSERT OR IGNORE INTO jobs(delivery_id,source_id,target_id,repository,run_id,head_sha,available_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, deliveryID, sourceID, targetID, repository, runID, headSHA, now, now)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (q *Queue) Claim(ctx context.Context) (Job, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var j Job
	err = tx.QueryRowContext(ctx, `SELECT delivery_id,source_id,target_id,repository,run_id,head_sha,attempts FROM jobs
WHERE status='queued' AND available_at<=? AND NOT EXISTS (SELECT 1 FROM jobs WHERE status='processing')
ORDER BY run_id DESC LIMIT 1`, time.Now().Unix()).Scan(&j.DeliveryID, &j.SourceID, &j.TargetID, &j.Repository, &j.RunID, &j.HeadSHA, &j.Attempts)
	if err != nil {
		return Job{}, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE jobs SET status='processing',attempts=attempts+1 WHERE delivery_id=? AND status='queued'`, j.DeliveryID)
	if err != nil {
		return Job{}, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return Job{}, errors.New("job was claimed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	j.Attempts++
	j.Status = "processing"
	return j, nil
}

func (q *Queue) Complete(ctx context.Context, deliveryID string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='done',last_error='' WHERE delivery_id=?`, deliveryID)
	return err
}

func (q *Queue) Retry(ctx context.Context, deliveryID, message string, delay time.Duration) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='queued',last_error=?,available_at=? WHERE delivery_id=?`, message, time.Now().Add(delay).Unix(), deliveryID)
	return err
}

func (q *Queue) Fail(ctx context.Context, deliveryID, message string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='failed',last_error=? WHERE delivery_id=?`, message, deliveryID)
	return err
}

func (q *Queue) Recover(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='queued',available_at=? WHERE status='processing'`, time.Now().Unix())
	return err
}

func (q *Queue) Pending(ctx context.Context) ([]Job, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT delivery_id,source_id,target_id,repository,run_id,head_sha,status,attempts,available_at,created_at,last_error
FROM jobs WHERE status IN ('queued','processing','failed') ORDER BY run_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.DeliveryID, &j.SourceID, &j.TargetID, &j.Repository, &j.RunID, &j.HeadSHA, &j.Status, &j.Attempts, &j.Available, &j.Created, &j.LastError); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (q *Queue) Peek(ctx context.Context) (Job, error) {
	var j Job
	err := q.db.QueryRowContext(ctx, `SELECT delivery_id,source_id,target_id,repository,run_id,head_sha,status,attempts,available_at,created_at,last_error
FROM jobs WHERE status='queued' ORDER BY run_id DESC LIMIT 1`).Scan(&j.DeliveryID, &j.SourceID, &j.TargetID, &j.Repository, &j.RunID, &j.HeadSHA, &j.Status, &j.Attempts, &j.Available, &j.Created, &j.LastError)
	return j, err
}

func (q *Queue) Drop(ctx context.Context, deliveryID string) (bool, error) {
	r, err := q.db.ExecContext(ctx, `DELETE FROM jobs WHERE delivery_id=? AND status='queued'`, deliveryID)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (q *Queue) Clear(ctx context.Context, keepNewest bool) (int64, error) {
	query := `DELETE FROM jobs WHERE status='queued'`
	if keepNewest {
		query = `DELETE FROM jobs WHERE status='queued' AND delivery_id NOT IN (SELECT delivery_id FROM jobs WHERE status='queued' ORDER BY run_id DESC LIMIT 1)`
	}
	r, err := q.db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (q *Queue) RefuseOlder(ctx context.Context, runID int64) (bool, error) {
	return q.RefuseOlderFor(ctx, "default", runID)
}

func (q *Queue) RefuseOlderFor(ctx context.Context, targetID string, runID int64) (bool, error) {
	var current int64
	err := q.db.QueryRowContext(ctx, `SELECT run_id FROM target_deployment_state WHERE target_id=?`, targetID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return runID <= current, err
}

func (q *Queue) MarkDeployed(ctx context.Context, runID int64, headSHA string) error {
	return q.MarkDeployedFor(ctx, "default", runID, headSHA)
}

func (q *Queue) MarkDeployedFor(ctx context.Context, targetID string, runID int64, headSHA string) error {
	_, err := q.db.ExecContext(ctx, `INSERT INTO target_deployment_state(target_id,run_id,head_sha,deployed_at) VALUES(?,?,?,?) ON CONFLICT(target_id) DO UPDATE SET run_id=excluded.run_id,head_sha=excluded.head_sha,deployed_at=excluded.deployed_at WHERE excluded.run_id>target_deployment_state.run_id`, targetID, runID, headSHA, time.Now().Unix())
	return err
}
