package orchestrator

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"github.com/joshjon/conduit-ci/pkg/constants"
	"github.com/joshjon/conduit-ci/pkg/docker"
	"github.com/joshjon/conduit-ci/pkg/github"
	"github.com/joshjon/conduit-ci/pkg/log"
	"github.com/joshjon/conduit-ci/sdk/conduit"
)

type Config struct {
	Namespace string
	Project   string
	Repo      github.RepoRef
}

type Orchestrator struct {
	cfg    Config
	logger log.Logger
	client client.Client
}

func NewOrchestrator(logger log.Logger, client client.Client, cfg Config) *Orchestrator {
	return &Orchestrator{
		cfg:    cfg,
		logger: logger,
		client: client,
	}
}

func (o *Orchestrator) Run(ctx context.Context) error {
	workDir := filepath.Join(
		os.TempDir(),
		"conduit",
		html.EscapeString(o.cfg.Repo.Ref),
		fmt.Sprintf("%s-%s", o.cfg.Project, uuid.Must(uuid.NewV7())),
	)

	repoPath := filepath.Join(workDir, "repo")
	o.logger.Info("fetching repository", "workdir", repoPath)
	sha, err := o.cfg.Repo.Fetch(ctx, repoPath)
	if err != nil {
		return err
	}
	o.logger.Info("successfully fetched repository", "sha", sha)

	defer func() {
		if rerr := os.RemoveAll(workDir); rerr != nil {
			o.logger.Warn("failed to remove temporary working directory", rerr, "workdir", repoPath)
		}
		o.logger.Info("removed repo workdir", "path", repoPath)
	}()

	// TODO: parse pipeline language from `filepath.Join(repoPath, ".conduit")

	o.logger.Info("starting go docker build runner container", "path", repoPath)

	runner, err := docker.StartGo(ctx, repoPath, ".conduit", nil, nil, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer runner.Kill(ctx)

	wfOpts := client.StartWorkflowOptions{TaskQueue: conduit.GetTaskQueue(o.cfg.Project, constants.ComponentPipeline)}
	o.logger.Info("executing pipeline workflow", "workflow.name", constants.WorkflowPipeline, "task_queue", wfOpts.TaskQueue)

	wr, err := o.client.ExecuteWorkflow(ctx, wfOpts, constants.WorkflowPipeline)
	if err != nil {
		return err
	}

	// TODO: query dependant modules and start the workers for them

	return wr.Get(ctx, nil)
}
