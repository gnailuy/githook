package main

import (
	"context"
	"flag"
	"fmt"
	gh "github.com/gnailuy/githook/internal/githook"
	targets "github.com/gnailuy/githook/internal/targets"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: githook <serve|worker|reconcile|deploy-run>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(fmt.Sprintf("resolve home directory: %v", err))
	}
	q, err := gh.OpenQueue(env("GITHOOK_DATABASE", filepath.Join(home, ".githook", "githook.db")))
	if err != nil {
		fatal(err.Error())
	}
	defer q.Close()
	g := gh.GitHub{BaseURL: os.Getenv("GITHUB_API_URL"), Repository: os.Getenv("GITHOOK_REPOSITORY"), Token: os.Getenv("GITHUB_TOKEN")}
	webRoot := filepath.Join(home, ".local", "share", "githook")
	d := gh.Deployer{ReleasesDir: env("GITHOOK_RELEASES", filepath.Join(webRoot, "releases")), CurrentLink: env("GITHOOK_CURRENT", filepath.Join(webRoot, "current")), SmokeURLs: split(os.Getenv("GITHOOK_SMOKE_URLS"))}
	w := gh.Worker{Queue: q, GitHub: g, Deployer: d, WorkflowName: os.Getenv("GITHOOK_WORKFLOW_NAME"), WorkflowPath: os.Getenv("GITHOOK_WORKFLOW_PATH"), Branch: env("GITHOOK_BRANCH", "main"), ArtifactPrefix: env("GITHOOK_ARTIFACT_PREFIX", "release-")}
	var multi *gh.MultiConfig
	if path := os.Getenv("GITHOOK_TARGETS_FILE"); path != "" {
		config, loadErr := gh.LoadMultiConfig(path)
		if loadErr != nil {
			fatal(loadErr.Error())
		}
		multi = &config
	}
	switch os.Args[1] {
	case "serve":
		if multi != nil {
			service, buildErr := multi.Service(q, os.Getenv)
			fatalIf(buildErr)
			fatalIf(gh.ListenAndServe(ctx, env("GITHOOK_LISTEN", "127.0.0.1:4000"), service))
			return
		}
		require("GITHOOK_REPOSITORY", g.Repository)
		require("GITHOOK_WEBHOOK_SECRET", os.Getenv("GITHOOK_WEBHOOK_SECRET"))
		r := gh.Receiver{Secret: []byte(os.Getenv("GITHOOK_WEBHOOK_SECRET")), Repository: g.Repository, Queue: q}
		s := gh.Service{WebhookPath: env("GITHOOK_WEBHOOK_PATH", gh.DefaultWebhookPath), Receiver: r, Queue: q}
		fatalIf(gh.ListenAndServe(ctx, env("GITHOOK_LISTEN", "127.0.0.1:4000"), s))
	case "worker":
		if multi != nil {
			registry, buildErr := targets.Build(*multi, os.Getenv)
			fatalIf(buildErr)
			fatalIf((gh.MultiWorker{Queue: q, Registry: registry}).Run(ctx))
			return
		}
		requireWorkerConfig(w)
		fatalIf(w.Run(ctx))
	case "deploy-run":
		fs := flag.NewFlagSet("deploy-run", flag.ExitOnError)
		sha := fs.String("sha", "", "expected full head SHA")
		sourceID := fs.String("source", "", "configured source id")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() != 1 || *sha == "" {
			fatal("usage: githook deploy-run [--source <id>] --sha <sha> <run-id>")
		}
		id, e := strconv.ParseInt(fs.Arg(0), 10, 64)
		fatalIf(e)
		if multi != nil {
			source, ok := multi.Source(*sourceID)
			if !ok {
				fatal("--source must name a configured source")
			}
			registry, buildErr := targets.Build(*multi, os.Getenv)
			fatalIf(buildErr)
			fatalIf((gh.MultiWorker{Queue: q, Registry: registry}).Process(ctx, gh.Job{SourceID: source.ID, TargetID: source.TargetID, Repository: source.Repository, RunID: id, HeadSHA: *sha}))
			return
		}
		requireWorkerConfig(w)
		fatalIf(w.ProcessRun(ctx, id, *sha))
	case "reconcile":
		if multi != nil {
			registry, buildErr := targets.Build(*multi, os.Getenv)
			fatalIf(buildErr)
			worker := gh.MultiWorker{Queue: q, Registry: registry}
			for _, source := range multi.Sources {
				job, latestErr := multi.LatestJob(ctx, source.ID, os.Getenv)
				fatalIf(latestErr)
				if old, stateErr := q.RefuseOlderFor(ctx, job.TargetID+":"+job.SourceID, job.RunID); stateErr != nil {
					fatalIf(stateErr)
				} else if old {
					continue
				}
				fatalIf(worker.Process(ctx, job))
			}
			return
		}
		requireWorkerConfig(w)
		fatalIf(reconcile(ctx, w))
	default:
		fatal("unknown command")
	}
}
func reconcile(ctx context.Context, w gh.Worker) error {
	r, err := w.GitHub.LatestSuccessfulRun(ctx, w.WorkflowPath, w.Branch)
	if err != nil {
		return err
	}
	return w.ProcessRun(ctx, r.ID, r.HeadSHA)
}
func requireWorkerConfig(w gh.Worker) {
	require("GITHUB_TOKEN", w.GitHub.Token)
	require("GITHOOK_REPOSITORY", w.GitHub.Repository)
	require("GITHOOK_WORKFLOW_NAME", w.WorkflowName)
	require("GITHOOK_WORKFLOW_PATH", w.WorkflowPath)
	require("GITHOOK_BRANCH", w.Branch)
	require("GITHOOK_ARTIFACT_PREFIX", w.ArtifactPrefix)
	if len(w.Deployer.SmokeURLs) == 0 {
		fatal("GITHOOK_SMOKE_URLS is required")
	}
}
func require(name, value string) {
	if value == "" {
		fatal(name + " is required")
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
func fatal(s string) { log.Fatal(s) }
func fatalIf(e error) {
	if e != nil {
		fatal(fmt.Sprint(e))
	}
}
