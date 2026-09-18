package githook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type MultiConfig struct {
	Sources []SourceConfig `json:"sources"`
	Targets []TargetConfig `json:"targets"`
}
type SourceConfig struct {
	ID               string `json:"id"`
	TargetID         string `json:"target_id"`
	Path             string `json:"path"`
	Repository       string `json:"repository"`
	WorkflowName     string `json:"workflow_name"`
	WorkflowPath     string `json:"workflow_path"`
	Branch           string `json:"branch"`
	ArtifactPrefix   string `json:"artifact_prefix"`
	Kind             string `json:"kind"`
	WebhookSecretEnv string `json:"webhook_secret_env"`
	GitHubTokenEnv   string `json:"github_token_env"`
}
type TargetConfig struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	ReleasesDir string   `json:"releases_dir"`
	CurrentLink string   `json:"current_link"`
	SmokeURLs   []string `json:"smoke_urls"`
	Service     string   `json:"service,omitempty"`
}

func LoadMultiConfig(path string) (MultiConfig, error) {
	file, err := os.Open(ExpandHome(path))
	if err != nil {
		return MultiConfig{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var config MultiConfig
	if err = decoder.Decode(&config); err != nil {
		return MultiConfig{}, fmt.Errorf("decode multi-target config: %w", err)
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return MultiConfig{}, fmt.Errorf("multi-target config must contain one JSON object")
	}
	if err = config.Validate(); err != nil {
		return MultiConfig{}, err
	}
	return config, nil
}
func (c MultiConfig) Validate() error {
	if len(c.Sources) == 0 || len(c.Targets) == 0 {
		return fmt.Errorf("at least one source and target are required")
	}
	targets := map[string]string{}
	paths := map[string]bool{}
	for _, target := range c.Targets {
		if !identityPattern.MatchString(target.ID) || (target.Kind != "static" && target.Kind != "sudoku-pair") || target.ReleasesDir == "" || target.CurrentLink == "" || len(target.SmokeURLs) == 0 || (target.Kind == "sudoku-pair" && !servicePattern.MatchString(target.Service)) {
			return fmt.Errorf("invalid target %q", target.ID)
		}
		if _, ok := targets[target.ID]; ok {
			return fmt.Errorf("duplicate target %q", target.ID)
		}
		targets[target.ID] = target.Kind
		for _, path := range []string{ExpandHome(target.ReleasesDir), ExpandHome(target.CurrentLink)} {
			if paths[path] {
				return fmt.Errorf("target release paths must be isolated")
			}
			paths[path] = true
		}
	}
	paths = map[string]bool{}
	sources := map[string]bool{}
	secretEnvs := map[string]bool{}
	counts := map[string]map[string]int{}
	for _, source := range c.Sources {
		if !identityPattern.MatchString(source.ID) || !identityPattern.MatchString(source.TargetID) || source.Path == "" || source.Path[0] != '/' || strings.HasPrefix(source.Path, queuePath) || source.Repository == "" || source.WorkflowName == "" || source.WorkflowPath == "" || source.Branch == "" || source.ArtifactPrefix == "" || !envPattern.MatchString(source.WebhookSecretEnv) || !envPattern.MatchString(source.GitHubTokenEnv) {
			return fmt.Errorf("invalid source %q", source.ID)
		}
		if sources[source.ID] || paths[source.Path] || secretEnvs[source.WebhookSecretEnv] {
			return fmt.Errorf("duplicate source, webhook path, or webhook secret identity")
		}
		sources[source.ID] = true
		paths[source.Path] = true
		secretEnvs[source.WebhookSecretEnv] = true
		kind, ok := targets[source.TargetID]
		if !ok {
			return fmt.Errorf("source %q uses unknown target", source.ID)
		}
		if kind == "static" && source.Kind != "static" || kind == "sudoku-pair" && source.Kind != "backend" && source.Kind != "frontend" {
			return fmt.Errorf("source %q kind does not match target", source.ID)
		}
		if counts[source.TargetID] == nil {
			counts[source.TargetID] = map[string]int{}
		}
		counts[source.TargetID][source.Kind]++
	}
	for id, kind := range targets {
		if kind == "static" && counts[id]["static"] != 1 {
			return fmt.Errorf("static target %q requires one source", id)
		}
		if kind == "sudoku-pair" && (counts[id]["backend"] != 1 || counts[id]["frontend"] != 1) {
			return fmt.Errorf("Sudoku target %q requires one backend and one frontend source", id)
		}
	}
	return nil
}
func (c MultiConfig) Source(id string) (SourceConfig, bool) {
	for _, source := range c.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return SourceConfig{}, false
}
func (c MultiConfig) Service(queue *Queue, getenv func(string) string) (Service, error) {
	routes := map[string]Receiver{}
	for _, source := range c.Sources {
		secret := getenv(source.WebhookSecretEnv)
		if secret == "" {
			return Service{}, fmt.Errorf("%s is required", source.WebhookSecretEnv)
		}
		routes[source.Path] = Receiver{Secret: []byte(secret), Repository: source.Repository, SourceID: source.ID, TargetID: source.TargetID, Queue: queue}
	}
	return Service{Routes: routes, Queue: queue}, nil
}
func (s SourceConfig) Worker(getenv func(string) string) (Worker, error) {
	token := getenv(s.GitHubTokenEnv)
	if token == "" {
		return Worker{}, fmt.Errorf("%s is required", s.GitHubTokenEnv)
	}
	return Worker{Queue: nil, GitHub: GitHub{Repository: s.Repository, Token: token}, WorkflowName: s.WorkflowName, WorkflowPath: s.WorkflowPath, Branch: s.Branch, ArtifactPrefix: s.ArtifactPrefix}, nil
}
func ExpandHome(path string) string {
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func (c MultiConfig) LatestJob(ctx context.Context, sourceID string, getenv func(string) string) (Job, error) {
	source, ok := c.Source(sourceID)
	if !ok {
		return Job{}, fmt.Errorf("unknown source %q", sourceID)
	}
	worker, err := source.Worker(getenv)
	if err != nil {
		return Job{}, err
	}
	run, err := worker.GitHub.LatestSuccessfulRun(ctx, source.WorkflowPath, source.Branch)
	if err != nil {
		return Job{}, err
	}
	return Job{SourceID: source.ID, TargetID: source.TargetID, Repository: source.Repository, RunID: run.ID, HeadSHA: run.HeadSHA}, nil
}
