package orchestrator

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"github.com/joshjon/conduit-ci/sdk/conduit"
	"github.com/joshjon/conduit-ci/sdk/internal/constants"
	"github.com/joshjon/conduit-ci/sdk/internal/docker"
	"github.com/joshjon/conduit-ci/sdk/internal/git"
)

type Config struct {
	Namespace string
	Project   string
	Repo      git.RepoRef
}

type Orchestrator struct {
	cfg    Config
	client client.Client
}

func NewOrchestrator(client client.Client, cfg Config) *Orchestrator {
	return &Orchestrator{
		cfg:    cfg,
		client: client,
	}
}

func (o *Orchestrator) Run(ctx context.Context, cfg Config) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	workDir := filepath.Join(
		os.TempDir(),
		"conduit",
		html.EscapeString(cfg.Repo.Ref),
		fmt.Sprintf("%s-%s", cfg.Project, id),
	)

	repoPath := filepath.Join(workDir, "repo")
	if _, err := cfg.Repo.Fetch(ctx, repoPath); err != nil {
		return err
	}
	defer os.RemoveAll(workDir

	repoConduitPath := filepath.Join(repoPath, ".conduit")

	// TODO: parse pipeline language

	runner, err := docker.StartGo(ctx, repoConduitPath, nil, nil, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer runner.Kill(ctx)

	wfOpts := client.StartWorkflowOptions{TaskQueue: conduit.GetTaskQueue(o.cfg.Project, constants.ComponentPipeline)}
	wr, err := o.client.ExecuteWorkflow(ctx, wfOpts, constants.WorkflowPipeline)
	if err != nil {
		return err
	}

	// TODO: query dependant modules and start the workers for them

	return wr.Get(ctx, nil)
}
