package githook

import (
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"testing"
)

func validConfig() MultiConfig {
	return MultiConfig{Sources: []SourceConfig{{ID: "site", TargetID: "site", Path: "/hooks/site", Repository: "owner/site", WorkflowName: "CI", WorkflowPath: ".github/workflows/ci.yml", Branch: "main", ArtifactPrefix: "release-", Kind: "static", WebhookSecretEnv: "SITE_SECRET", GitHubTokenEnv: "SITE_TOKEN"}, {ID: "backend", TargetID: "sudoku", Path: "/hooks/backend", Repository: "gnailuy/sudoku", WorkflowName: "CI", WorkflowPath: ".github/workflows/ci.yml", Branch: "main", ArtifactPrefix: "sudoku-backend-", Kind: "backend", WebhookSecretEnv: "BACKEND_SECRET", GitHubTokenEnv: "SUDOKU_TOKEN"}, {ID: "frontend", TargetID: "sudoku", Path: "/hooks/frontend", Repository: "gnailuy/sudoku-ui", WorkflowName: "CI", WorkflowPath: ".github/workflows/ci.yml", Branch: "master", ArtifactPrefix: "sudoku-frontend-", Kind: "frontend", WebhookSecretEnv: "FRONTEND_SECRET", GitHubTokenEnv: "SUDOKU_TOKEN"}}, Targets: []TargetConfig{{ID: "site", Kind: "static", ReleasesDir: "~/site/releases", CurrentLink: "~/site/current", SmokeURLs: []string{"http://127.0.0.1/site"}}, {ID: "sudoku", Kind: "sudoku-pair", Service: "sudoku-api.service", ReleasesDir: "~/sudoku/releases", CurrentLink: "~/sudoku/current", SmokeURLs: []string{"http://127.0.0.1/sudoku/healthz"}}}}
}
func TestMultiConfigValidatesIsolationAndSecrets(t *testing.T) {
	config := validConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SITE_SECRET": "a", "BACKEND_SECRET": "b", "FRONTEND_SECRET": "c", "SITE_TOKEN": "d", "SUDOKU_TOKEN": "e"}
	getenv := func(key string) string { return env[key] }
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	service, err := config.Service(q, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.Routes) != 3 {
		t.Fatalf("routes=%d", len(service.Routes))
	}
	config.Sources[2].Path = "/hooks/backend"
	if err := config.Validate(); err == nil {
		t.Fatal("duplicate exact route accepted")
	}
}
func TestOpenQueueMigratesLegacySingleTargetDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE jobs (delivery_id TEXT PRIMARY KEY, run_id INTEGER NOT NULL UNIQUE, head_sha TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'queued', attempts INTEGER NOT NULL DEFAULT 0, available_at INTEGER NOT NULL, created_at INTEGER NOT NULL, last_error TEXT NOT NULL DEFAULT ''); CREATE TABLE deployment_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), run_id INTEGER NOT NULL, head_sha TEXT NOT NULL, deployed_at INTEGER NOT NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	q, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	added, err := q.EnqueueFor(context.Background(), "52345678-1234-1234-1234-123456789abc", "backend", "sudoku", "gnailuy/sudoku", 42, "0123456789012345678901234567890123456789")
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	job, err := q.Claim(context.Background())
	if err != nil || job.TargetID != "sudoku" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
}
func TestLoadMultiConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(path, []byte(`{"sources":[],"targets":[],"secret":"no"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMultiConfig(path); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestShippedMultiTargetExampleBuildsBothProcesses(t *testing.T) {
	config, err := LoadMultiConfig(filepath.Join("..", "..", "packaging", "config", "targets.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GITHOOK_SITE_WEBHOOK_SECRET": "a", "GITHOOK_SUDOKU_BACKEND_SECRET": "b", "GITHOOK_SUDOKU_FRONTEND_SECRET": "c", "GITHUB_SITE_TOKEN": "d", "GITHUB_SUDOKU_TOKEN": "e"}
	getenv := func(key string) string { return env[key] }
	q, err := OpenQueue(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, err = config.Service(q, getenv); err != nil {
		t.Fatal(err)
	}
}
