package static

import (
	"context"

	gh "github.com/gnailuy/githook/internal/githook"
)

type Adapter struct{ Worker gh.Worker }

func (a Adapter) Process(ctx context.Context, job gh.Job) error {
	worker := a.Worker
	worker.Queue = nil
	worker.GitHub.Repository = job.Repository
	return worker.ProcessRun(ctx, job.RunID, job.HeadSHA)
}
