package targets

import (
	"fmt"

	gh "github.com/gnailuy/githook/internal/githook"
	"github.com/gnailuy/githook/internal/targets/static"
	"github.com/gnailuy/githook/internal/targets/sudoku"
)

func Build(config gh.MultiConfig, getenv func(string) string) (*gh.AdapterRegistry, error) {
	registry := gh.NewAdapterRegistry()
	byTarget := map[string][]gh.SourceConfig{}
	for _, source := range config.Sources {
		byTarget[source.TargetID] = append(byTarget[source.TargetID], source)
	}
	for _, target := range config.Targets {
		var adapter gh.TargetAdapter
		switch target.Kind {
		case "static":
			source := byTarget[target.ID][0]
			worker, err := source.Worker(getenv)
			if err != nil {
				return nil, err
			}
			worker.Deployer = gh.Deployer{ReleasesDir: gh.ExpandHome(target.ReleasesDir), CurrentLink: gh.ExpandHome(target.CurrentLink), SmokeURLs: target.SmokeURLs}
			adapter = static.Adapter{Worker: worker}
		case "sudoku-pair":
			var backend, frontend sudoku.SudokuSource
			for _, source := range byTarget[target.ID] {
				worker, err := source.Worker(getenv)
				if err != nil {
					return nil, err
				}
				spec := sudoku.SudokuSource{ID: source.ID, Repository: source.Repository, WorkflowName: source.WorkflowName, WorkflowPath: source.WorkflowPath, Branch: source.Branch, ArtifactPrefix: source.ArtifactPrefix, Kind: source.Kind, GitHub: worker.GitHub}
				if spec.Kind == "backend" {
					backend = spec
				} else {
					frontend = spec
				}
			}
			adapter = sudoku.Adapter{Resolver: sudoku.GitHubPairResolver{Backend: backend, Frontend: frontend}, Activator: sudoku.DirectoryActivator{ReleasesDir: gh.ExpandHome(target.ReleasesDir), CurrentLink: gh.ExpandHome(target.CurrentLink), SmokeURLs: target.SmokeURLs, Service: sudoku.UserSystemdService{Name: target.Service}}}
		default:
			return nil, fmt.Errorf("unsupported target kind %q", target.Kind)
		}
		if err := registry.RegisterTarget(target.ID, adapter); err != nil {
			return nil, err
		}
	}
	for _, source := range config.Sources {
		if err := registry.RegisterSource(source.ID, source.TargetID); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
