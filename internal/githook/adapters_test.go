package githook

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type recordingAdapter struct {
	jobs []Job
	err  error
}

func (a *recordingAdapter) Process(_ context.Context, job Job) error {
	a.jobs = append(a.jobs, job)
	return a.err
}

func TestAdapterRegistryIsolatesSourceAndTarget(t *testing.T) {
	r := NewAdapterRegistry()
	blog := &recordingAdapter{}
	sudoku := &recordingAdapter{}
	if err := r.RegisterTarget("blog", blog); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterTarget("sudoku", sudoku); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSource("blog_workflow", "blog"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSource("sudoku_backend", "sudoku"); err != nil {
		t.Fatal(err)
	}
	job := Job{SourceID: "sudoku_backend", TargetID: "sudoku", Repository: "gnailuy/sudoku", RunID: 42}
	if err := r.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(sudoku.jobs) != 1 || len(blog.jobs) != 0 {
		t.Fatalf("blog=%d sudoku=%d", len(blog.jobs), len(sudoku.jobs))
	}
	job.TargetID = "blog"
	if err := r.Process(context.Background(), job); err == nil || !isPermanent(err) {
		t.Fatalf("cross-target job accepted: %v", err)
	}
}

func TestMultiWorkerTracksDeploymentPerTarget(t *testing.T) {
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	r := NewAdapterRegistry()
	a := &recordingAdapter{}
	_ = r.RegisterTarget("sudoku", a)
	_ = r.RegisterSource("backend", "sudoku")
	job := Job{SourceID: "backend", TargetID: "sudoku", Repository: "gnailuy/sudoku", RunID: 42, HeadSHA: "0123456789012345678901234567890123456789"}
	w := MultiWorker{Queue: q, Registry: r}
	if err := w.Process(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if old, err := q.RefuseOlderFor(context.Background(), "sudoku:backend", 42); err != nil || !old {
		t.Fatalf("old=%v err=%v", old, err)
	}
	if old, err := q.RefuseOlderFor(context.Background(), "blog", 42); err != nil || old {
		t.Fatalf("target state leaked old=%v err=%v", old, err)
	}
	a.err = errors.New("failed")
	job.RunID = 43
	if err := w.Process(context.Background(), job); err == nil {
		t.Fatal("adapter failure ignored")
	}
}
