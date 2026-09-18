package sudoku

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gh "github.com/gnailuy/githook/internal/githook"
)

type fakeServiceController struct {
	restarts int
	err      error
}

func (s *fakeServiceController) Restart(context.Context) error { s.restarts++; return s.err }

func componentZip(t *testing.T, kind, repository, sha string, runID int64) []byte {
	t.Helper()
	files := map[string][]byte{}
	var manifest any
	if kind == "backend" {
		body := []byte("binary")
		files["sudoku"] = body
		manifest = backendManifest{Schema: "sudoku-backend-release/v1", Repository: repository, Workflow: "CI", RunID: runID, Commit: sha, Executable: "sudoku", SHA256: sha256Hex(body)}
	} else {
		html := []byte("<html>Sudoku</html>")
		js := []byte("app")
		files["site/index.html"] = html
		files["site/assets/app.js"] = js
		manifest = frontendManifest{Schema: "sudoku-frontend-release/v1", Repository: repository, Workflow: "CI", RunID: runID, Commit: sha, MountPath: "/sudoku", EntryPoint: "site/index.html", Files: []frontendFile{{Path: "site/assets/app.js", SHA256: sha256Hex(js)}, {Path: "site/index.html", SHA256: sha256Hex(html)}}}
	}
	manifestBytes, _ := json.Marshal(manifest)
	files["manifest.json"] = manifestBytes
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range sortedFileNames(files) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write(files[name])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func source(kind, repository, id string) SudokuSource {
	return SudokuSource{ID: id, Kind: kind, Repository: repository, WorkflowName: "CI", WorkflowPath: ".github/workflows/ci.yml", Branch: map[bool]string{true: "main", false: "master"}[kind == "backend"], ArtifactPrefix: "sudoku-" + kind + "-"}
}
func runFor(repository, sha string, id int64, branch string) gh.Run {
	var run gh.Run
	run.ID = id
	run.Name = "CI"
	run.Path = ".github/workflows/ci.yml"
	run.Event = "push"
	run.HeadBranch = branch
	run.HeadSHA = sha
	run.Status = "completed"
	run.Conclusion = "success"
	run.Repository.FullName = repository
	return run
}
func TestVerifySudokuComponentsAndActivatePair(t *testing.T) {
	backendSHA := "0123456789012345678901234567890123456789"
	frontendSHA := "1123456789012345678901234567890123456789"
	backendSource := source("backend", "gnailuy/sudoku", "backend")
	frontendSource := source("frontend", "gnailuy/sudoku-ui", "frontend")
	backend, err := verifySudokuComponent(componentZip(t, "backend", backendSource.Repository, backendSHA, 41), backendSource, runFor(backendSource.Repository, backendSHA, 41, "main"))
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := verifySudokuComponent(componentZip(t, "frontend", frontendSource.Repository, frontendSHA, 42), frontendSource, runFor(frontendSource.Repository, frontendSHA, 42, "master"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, _ os.FileInfo, _ error) error { _ = os.Chmod(path, 0700); return nil })
	})
	releases := filepath.Join(root, "releases")
	current := filepath.Join(root, "current")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	service := &fakeServiceController{}
	activator := DirectoryActivator{ReleasesDir: releases, CurrentLink: current, SmokeURLs: []string{server.URL}, Service: service}
	pair := SudokuPair{Backend: backend, Frontend: frontend}
	if err = activator.Activate(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(current)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"backend/sudoku", "frontend/index.html", "frontend/assets/app.js", "pair.json"} {
		if _, err = os.Stat(filepath.Join(target, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if err = activator.Activate(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	if service.restarts != 1 {
		t.Fatalf("idempotent activation restarted service %d times", service.restarts)
	}
}
func TestSudokuActivationRollbackDoesNotTouchNeighbor(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, _ os.FileInfo, _ error) error { _ = os.Chmod(path, 0700); return nil })
	})
	neighbor := filepath.Join(root, "blog-current")
	if err := os.Symlink("/unchanged/blog", neighbor); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(root, "previous")
	if err := os.MkdirAll(previous, 0755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "sudoku-current")
	if err := os.Symlink(previous, current); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer server.Close()
	service := &fakeServiceController{}
	backendSHA := "0123456789012345678901234567890123456789"
	frontendSHA := "1123456789012345678901234567890123456789"
	pair := SudokuPair{Backend: SudokuComponent{SourceID: "backend", Repository: "gnailuy/sudoku", Kind: "backend", RunID: 1, SHA: backendSHA, Files: map[string][]byte{"sudoku": []byte("binary")}}, Frontend: SudokuComponent{SourceID: "frontend", Repository: "gnailuy/sudoku-ui", Kind: "frontend", RunID: 2, SHA: frontendSHA, Files: map[string][]byte{"site/index.html": []byte("html")}}}
	err := (DirectoryActivator{ReleasesDir: filepath.Join(root, "releases"), CurrentLink: current, SmokeURLs: []string{server.URL}, Service: service}).Activate(context.Background(), pair)
	if err == nil {
		t.Fatal("smoke failure accepted")
	}
	got, _ := os.Readlink(current)
	if got != previous {
		t.Fatalf("rollback target=%q want=%q", got, previous)
	}
	blog, _ := os.Readlink(neighbor)
	if blog != "/unchanged/blog" {
		t.Fatalf("neighbor changed to %q", blog)
	}
	if service.restarts != 2 {
		t.Fatalf("service restarts=%d want 2", service.restarts)
	}
}
func TestVerifySudokuComponentRejectsWrongManifestAndChecksum(t *testing.T) {
	sha := "0123456789012345678901234567890123456789"
	spec := source("backend", "gnailuy/sudoku", "backend")
	run := runFor(spec.Repository, sha, 41, "main")
	data := componentZip(t, "backend", spec.Repository, sha, 41)
	if _, err := verifySudokuComponent(data, spec, run); err != nil {
		t.Fatal(err)
	}
	run.ID = 99
	if _, err := verifySudokuComponent(data, spec, run); err == nil || !gh.IsPermanent(err) {
		t.Fatalf("wrong run accepted: %v", err)
	}
}

func TestGitHubPairResolverUsesTriggeredAndNewestCounterpartRuns(t *testing.T) {
	backendSHA := "0123456789012345678901234567890123456789"
	frontendSHA := "1123456789012345678901234567890123456789"
	backendRun := runFor("gnailuy/sudoku", backendSHA, 100, "main")
	frontendRun := runFor("gnailuy/sudoku-ui", frontendSHA, 90, "master")
	backendZip := componentZip(t, "backend", "gnailuy/sudoku", backendSHA, 100)
	frontendZip := componentZip(t, "frontend", "gnailuy/sudoku-ui", frontendSHA, 90)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/gnailuy/sudoku/actions/runs/100":
			_ = json.NewEncoder(w).Encode(backendRun)
		case r.URL.Path == "/repos/gnailuy/sudoku-ui/actions/workflows/.github/workflows/ci.yml/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []gh.Run{frontendRun}})
		case r.URL.Path == "/repos/gnailuy/sudoku/actions/runs/100/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": []gh.Artifact{{ID: 1, Name: "sudoku-backend-" + backendSHA, ExpiresAt: time.Now().Add(time.Hour)}}})
		case r.URL.Path == "/repos/gnailuy/sudoku-ui/actions/runs/90/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{"artifacts": []gh.Artifact{{ID: 2, Name: "sudoku-frontend-" + frontendSHA, ExpiresAt: time.Now().Add(time.Hour)}}})
		case strings.HasSuffix(r.URL.Path, "/actions/artifacts/1/zip"):
			_, _ = w.Write(backendZip)
		case strings.HasSuffix(r.URL.Path, "/actions/artifacts/2/zip"):
			_, _ = w.Write(frontendZip)
		default:
			t.Fatalf("unexpected GitHub request %s", r.URL.String())
		}
	}))
	defer server.Close()
	backend := source("backend", "gnailuy/sudoku", "backend")
	frontend := source("frontend", "gnailuy/sudoku-ui", "frontend")
	backend.GitHub.BaseURL, frontend.GitHub.BaseURL = server.URL, server.URL
	pair, err := (GitHubPairResolver{Backend: backend, Frontend: frontend}).Resolve(context.Background(), gh.Job{SourceID: "backend", TargetID: "sudoku", Repository: "gnailuy/sudoku", RunID: 100, HeadSHA: backendSHA})
	if err != nil {
		t.Fatal(err)
	}
	if pair.Backend.RunID != 100 || pair.Frontend.RunID != 90 {
		t.Fatalf("unexpected pair: %+v", pair)
	}
}
