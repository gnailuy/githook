package targets

import (
	gh "github.com/gnailuy/githook/internal/githook"
	"path/filepath"
	"testing"
)

func TestShippedConfigBuildsTypedRegistry(t *testing.T) {
	config, err := gh.LoadMultiConfig(filepath.Join("..", "..", "packaging", "config", "targets.json.example"))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GITHOOK_SITE_WEBHOOK_SECRET": "a", "GITHOOK_SUDOKU_BACKEND_SECRET": "b", "GITHOOK_SUDOKU_FRONTEND_SECRET": "c", "GITHUB_SITE_TOKEN": "d", "GITHUB_SUDOKU_TOKEN": "e"}
	registry, err := Build(config, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("registry is nil")
	}
}
