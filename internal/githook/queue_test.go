package githook

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenQueueCreatesPrivateParentDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), ".githook")
	q, err := OpenQueue(filepath.Join(parent, "githook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("queue directory mode = %o, want 700", got)
	}
}

func TestQueueDeduplicatesAndRecovers(t *testing.T) {
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	sha := "0123456789012345678901234567890123456789"
	added, err := q.Enqueue(ctx, "12345678-1234-1234-1234-123456789abc", 9, sha)
	if err != nil || !added {
		t.Fatalf("enqueue %v %v", added, err)
	}
	added, err = q.Enqueue(ctx, "22345678-1234-1234-1234-123456789abc", 9, sha)
	if err != nil || added {
		t.Fatalf("duplicate %v %v", added, err)
	}
	j, err := q.Claim(ctx)
	if err != nil || j.RunID != 9 {
		t.Fatalf("claim %+v %v", j, err)
	}
	if err = q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Claim(ctx); err != sql.ErrNoRows {
		t.Fatalf("got %v", err)
	}
	if old, err := q.RefuseOlder(ctx, 9); err != nil || old {
		t.Fatalf("empty state old=%v err=%v", old, err)
	}
	if err := q.MarkDeployed(ctx, 9, sha); err != nil {
		t.Fatal(err)
	}
	if old, err := q.RefuseOlder(ctx, 8); err != nil || !old {
		t.Fatalf("older run accepted old=%v err=%v", old, err)
	}
	if old, err := q.RefuseOlder(ctx, 10); err != nil || old {
		t.Fatalf("newer run refused old=%v err=%v", old, err)
	}
}

func TestQueueRetainsFailedJobsForInspection(t *testing.T) {
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	const deliveryID = "12345678-1234-1234-1234-123456789abc"
	if added, err := q.Enqueue(ctx, deliveryID, 9, "0123456789012345678901234567890123456789"); err != nil || !added {
		t.Fatalf("enqueue %v %v", added, err)
	}
	j, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Fail(ctx, j.DeliveryID, "permanent failure"); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.Pending(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "failed" || jobs[0].LastError != "permanent failure" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	if _, err := q.Claim(ctx); err != sql.ErrNoRows {
		t.Fatalf("failed job was claimable: %v", err)
	}
}

func TestQueueCarriesSourceAndTargetIdentity(t *testing.T) {
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	sha := "0123456789012345678901234567890123456789"
	added, err := q.EnqueueFor(ctx, "42345678-1234-1234-1234-123456789abc", "backend", "sudoku", "gnailuy/sudoku", 42, sha)
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job.SourceID != "backend" || job.TargetID != "sudoku" || job.Repository != "gnailuy/sudoku" {
		t.Fatalf("identity lost: %+v", job)
	}
}
