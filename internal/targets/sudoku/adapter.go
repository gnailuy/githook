package sudoku

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"

	gh "github.com/gnailuy/githook/internal/githook"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type SudokuSource struct {
	ID, Repository, WorkflowName, WorkflowPath, Branch, ArtifactPrefix, Kind string
	GitHub                                                                   gh.GitHub
}

type SudokuComponent struct {
	SourceID, Repository, Kind string
	RunID                      int64
	SHA                        string
	Files                      map[string][]byte
	Checksums                  map[string]string
	MountPath                  string
}

type SudokuPair struct{ Backend, Frontend SudokuComponent }

type SudokuPairResolver interface {
	Resolve(context.Context, gh.Job) (SudokuPair, error)
}
type SudokuPairActivator interface {
	Activate(context.Context, SudokuPair) error
}

type Adapter struct {
	Resolver  SudokuPairResolver
	Activator SudokuPairActivator
}

func (a Adapter) Process(ctx context.Context, job gh.Job) error {
	if a.Resolver == nil || a.Activator == nil {
		return gh.Permanent(fmt.Errorf("Sudoku resolver and activator are required"))
	}
	pair, err := a.Resolver.Resolve(ctx, job)
	if err != nil {
		return err
	}
	if err = validateSudokuPair(pair); err != nil {
		return gh.Permanent(err)
	}
	return a.Activator.Activate(ctx, pair)
}

type GitHubPairResolver struct{ Backend, Frontend SudokuSource }

func (r GitHubPairResolver) Resolve(ctx context.Context, trigger gh.Job) (SudokuPair, error) {
	if r.Backend.Kind != "backend" || r.Frontend.Kind != "frontend" {
		return SudokuPair{}, gh.Permanent(fmt.Errorf("one backend and one frontend source are required"))
	}
	if trigger.SourceID != r.Backend.ID && trigger.SourceID != r.Frontend.ID {
		return SudokuPair{}, gh.Permanent(fmt.Errorf("trigger source is not part of Sudoku target"))
	}
	backend, err := r.component(ctx, r.Backend, trigger)
	if err != nil {
		return SudokuPair{}, err
	}
	frontend, err := r.component(ctx, r.Frontend, trigger)
	if err != nil {
		return SudokuPair{}, err
	}
	return SudokuPair{Backend: backend, Frontend: frontend}, nil
}
func (r GitHubPairResolver) component(ctx context.Context, source SudokuSource, trigger gh.Job) (SudokuComponent, error) {
	g := source.GitHub
	g.Repository = source.Repository
	var run gh.Run
	var err error
	if trigger.SourceID == source.ID {
		run, err = g.Run(ctx, trigger.RunID)
	} else {
		run, err = g.LatestSuccessfulRun(ctx, source.WorkflowPath, source.Branch)
	}
	if err != nil {
		return SudokuComponent{}, err
	}
	expectedSHA := run.HeadSHA
	if trigger.SourceID == source.ID {
		expectedSHA = trigger.HeadSHA
	}
	if run.Repository.FullName != source.Repository || run.Name != source.WorkflowName || run.Path != source.WorkflowPath || run.Event != "push" || run.HeadBranch != source.Branch || run.Status != "completed" || run.Conclusion != "success" || !strings.EqualFold(run.HeadSHA, expectedSHA) {
		return SudokuComponent{}, gh.Permanent(fmt.Errorf("%s run metadata is not eligible", source.ID))
	}
	artifact, err := g.Artifact(ctx, run.ID, source.ArtifactPrefix+strings.ToLower(run.HeadSHA))
	if err != nil {
		return SudokuComponent{}, err
	}
	data, err := g.Download(ctx, artifact.ID)
	if err != nil {
		return SudokuComponent{}, err
	}
	if strings.HasPrefix(artifact.Digest, "sha256:") {
		if err = verifyDigest(data, strings.TrimPrefix(artifact.Digest, "sha256:")); err != nil {
			return SudokuComponent{}, gh.Permanent(err)
		}
	}
	return verifySudokuComponent(data, source, run)
}

type backendManifest struct {
	Schema     string `json:"schema"`
	Repository string `json:"repository"`
	Workflow   string `json:"workflow"`
	RunID      int64  `json:"run_id"`
	Commit     string `json:"commit"`
	Executable string `json:"executable"`
	SHA256     string `json:"sha256"`
}
type frontendFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type frontendManifest struct {
	Schema     string         `json:"schema"`
	Repository string         `json:"repository"`
	Workflow   string         `json:"workflow"`
	RunID      int64          `json:"run_id"`
	Commit     string         `json:"commit"`
	MountPath  string         `json:"mount_path"`
	EntryPoint string         `json:"entry_point"`
	Files      []frontendFile `json:"files"`
}

func verifySudokuComponent(data []byte, source SudokuSource, run gh.Run) (SudokuComponent, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return SudokuComponent{}, gh.Permanent(fmt.Errorf("%s artifact zip: %w", source.ID, err))
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !safePath(file.Name) || file.Mode()&os.ModeType != 0 {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("unsafe component path %q", file.Name))
		}
		if _, ok := files[file.Name]; ok {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("duplicate component path %q", file.Name))
		}
		rc, e := file.Open()
		if e != nil {
			return SudokuComponent{}, e
		}
		body, e := io.ReadAll(io.LimitReader(rc, 512<<20))
		rc.Close()
		if e != nil {
			return SudokuComponent{}, e
		}
		files[file.Name] = body
	}
	manifest, ok := files["manifest.json"]
	checksums := map[string]string{}
	mountPath := ""
	if !ok {
		return SudokuComponent{}, gh.Permanent(fmt.Errorf("manifest missing"))
	}
	switch source.Kind {
	case "backend":
		var m backendManifest
		if err = json.Unmarshal(manifest, &m); err != nil {
			return SudokuComponent{}, gh.Permanent(err)
		}
		if m.Schema != "sudoku-backend-release/v1" || m.Repository != source.Repository || m.Workflow != source.WorkflowName || m.RunID != run.ID || !strings.EqualFold(m.Commit, run.HeadSHA) || m.Executable != "sudoku" {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("backend manifest identity mismatch"))
		}
		binary, ok := files[m.Executable]
		if !ok || !strings.EqualFold(sha256Hex(binary), m.SHA256) {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("backend checksum mismatch"))
		}
		if len(files) != 2 {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("unexpected backend artifact files"))
		}
		checksums["sudoku"] = m.SHA256
	case "frontend":
		var m frontendManifest
		if err = json.Unmarshal(manifest, &m); err != nil {
			return SudokuComponent{}, gh.Permanent(err)
		}
		if m.Schema != "sudoku-frontend-release/v1" || m.Repository != source.Repository || m.Workflow != source.WorkflowName || m.RunID != run.ID || !strings.EqualFold(m.Commit, run.HeadSHA) || m.EntryPoint != "site/index.html" {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("frontend manifest identity mismatch"))
		}
		if m.MountPath != "" && (!strings.HasPrefix(m.MountPath, "/") || strings.HasSuffix(m.MountPath, "/") || strings.Contains(m.MountPath, "//") || strings.Contains(m.MountPath, "..")) {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("unsafe frontend mount"))
		}
		want := map[string]string{}
		for _, entry := range m.Files {
			if !safePath(entry.Path) || !strings.HasPrefix(entry.Path, "site/") {
				return SudokuComponent{}, gh.Permanent(fmt.Errorf("unsafe frontend inventory path"))
			}
			if _, ok := want[entry.Path]; ok {
				return SudokuComponent{}, gh.Permanent(fmt.Errorf("duplicate frontend inventory path"))
			}
			want[entry.Path] = entry.SHA256
		}
		if _, ok := want[m.EntryPoint]; !ok {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("frontend entry point missing"))
		}
		if len(files) != len(want)+1 {
			return SudokuComponent{}, gh.Permanent(fmt.Errorf("frontend inventory mismatch"))
		}
		mountPath = m.MountPath
		for path, sum := range want {
			body, ok := files[path]
			if !ok || !strings.EqualFold(sha256Hex(body), sum) {
				return SudokuComponent{}, gh.Permanent(fmt.Errorf("frontend checksum mismatch for %s", path))
			}
			checksums[path] = sum
		}
	default:
		return SudokuComponent{}, gh.Permanent(fmt.Errorf("unknown Sudoku component kind"))
	}
	delete(files, "manifest.json")
	return SudokuComponent{SourceID: source.ID, Repository: source.Repository, Kind: source.Kind, RunID: run.ID, SHA: strings.ToLower(run.HeadSHA), Files: files, Checksums: checksums, MountPath: mountPath}, nil
}
func validateSudokuPair(pair SudokuPair) error {
	if pair.Backend.Kind != "backend" || pair.Backend.Repository != "gnailuy/sudoku" || pair.Frontend.Kind != "frontend" || pair.Frontend.Repository != "gnailuy/sudoku-ui" {
		return fmt.Errorf("paired component identities are invalid")
	}
	if !validSHA(pair.Backend.SHA) || !validSHA(pair.Frontend.SHA) || pair.Backend.RunID <= 0 || pair.Frontend.RunID <= 0 {
		return fmt.Errorf("paired run identity is invalid")
	}
	if len(pair.Backend.Files) != 1 || pair.Backend.Files["sudoku"] == nil || pair.Frontend.Files["site/index.html"] == nil {
		return fmt.Errorf("paired component contents are incomplete")
	}
	return nil
}

type ServiceController interface{ Restart(context.Context) error }
type UserSystemdService struct{ Name string }

func (s UserSystemdService) Restart(ctx context.Context) error {
	if !servicePattern.MatchString(s.Name) {
		return fmt.Errorf("invalid Sudoku service name")
	}
	if output, err := exec.CommandContext(ctx, "systemctl", "--user", "restart", s.Name).CombinedOutput(); err != nil {
		return fmt.Errorf("restart %s: %w: %s", s.Name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

type DirectoryActivator struct {
	ReleasesDir, CurrentLink string
	SmokeURLs                []string
	Service                  ServiceController
}

func (a DirectoryActivator) Activate(ctx context.Context, pair SudokuPair) error {
	if err := validateSudokuPair(pair); err != nil {
		return err
	}
	if a.ReleasesDir == "" || a.CurrentLink == "" {
		return fmt.Errorf("Sudoku release paths are required")
	}
	id := pair.Backend.SHA[:12] + "-" + pair.Frontend.SHA[:12]
	target := filepath.Join(a.ReleasesDir, id)
	if err := os.MkdirAll(a.ReleasesDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.CurrentLink), 0755); err != nil {
		return err
	}
	if current, err := filepath.EvalSymlinks(a.CurrentLink); err == nil && current == target {
		return nil
	}
	_, targetErr := os.Lstat(target)
	if targetErr == nil {
		return a.activateExisting(ctx, target)
	}
	if !os.IsNotExist(targetErr) {
		return targetErr
	}
	tmp, err := os.MkdirTemp(a.ReleasesDir, ".incoming-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, component := range []SudokuComponent{pair.Backend, pair.Frontend} {
		base := component.Kind
		for name, body := range component.Files {
			relative := name
			if component.Kind == "frontend" {
				relative = strings.TrimPrefix(name, "site/")
			}
			if !safePath(relative) {
				return fmt.Errorf("unsafe staged path")
			}
			path := filepath.Join(tmp, base, filepath.FromSlash(relative))
			if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0444)
			if component.Kind == "backend" {
				mode = 0555
			}
			if err = os.WriteFile(path, body, mode); err != nil {
				return err
			}
		}
	}
	pairRecord, _ := json.MarshalIndent(map[string]any{"schema": "sudoku-pair/v1", "backend": map[string]any{"repository": pair.Backend.Repository, "run_id": pair.Backend.RunID, "commit": pair.Backend.SHA, "checksums": pair.Backend.Checksums}, "frontend": map[string]any{"repository": pair.Frontend.Repository, "run_id": pair.Frontend.RunID, "commit": pair.Frontend.SHA, "mount_path": pair.Frontend.MountPath, "checksums": pair.Frontend.Checksums}}, "", "  ")
	if err = os.WriteFile(filepath.Join(tmp, "pair.json"), append(pairRecord, '\n'), 0444); err != nil {
		return err
	}
	if err = sealRelease(tmp); err != nil {
		return err
	}
	if err = os.Rename(tmp, target); err != nil {
		return err
	}
	return a.activateExisting(ctx, target)
}
func (a DirectoryActivator) activateExisting(ctx context.Context, target string) error {
	var err error
	previous, _ := os.Readlink(a.CurrentLink)
	link := a.CurrentLink + ".new"
	_ = os.Remove(link)
	if err = os.Symlink(target, link); err != nil {
		return err
	}
	if err = os.Rename(link, a.CurrentLink); err != nil {
		return err
	}
	if a.Service != nil {
		if err = a.Service.Restart(ctx); err != nil {
			return a.rollback(previous, err)
		}
	}
	if err = smoke(ctx, a.SmokeURLs); err != nil {
		return a.rollback(previous, err)
	}
	return nil
}
func (a DirectoryActivator) rollback(previous string, cause error) error {
	if previous != "" {
		rollback := a.CurrentLink + ".rollback"
		_ = os.Remove(rollback)
		if err := os.Symlink(previous, rollback); err == nil {
			_ = os.Rename(rollback, a.CurrentLink)
		}
	} else {
		_ = os.Remove(a.CurrentLink)
	}
	if a.Service != nil {
		_ = a.Service.Restart(context.Background())
	}
	return fmt.Errorf("Sudoku pair activation failed and previous pair was restored: %w", cause)
}

func validSHA(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 40 && err == nil
}
func safePath(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && filepath.ToSlash(filepath.Clean(name)) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func verifyDigest(data []byte, want string) error {
	if !strings.EqualFold(sha256Hex(data), want) {
		return fmt.Errorf("GitHub artifact digest mismatch")
	}
	return nil
}
func sealRelease(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0555)
		}
		mode := os.FileMode(0444)
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode().Perm()&0111 != 0 {
			mode = 0555
		}
		return os.Chmod(path, mode)
	})
}

var servicePattern = regexp.MustCompile(`^[a-zA-Z0-9_.@-]+\.service$`)

func smoke(ctx context.Context, urls []string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	for _, url := range urls {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return fmt.Errorf("%s returned %s", url, response.Status)
		}
	}
	return nil
}

func sortedFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
