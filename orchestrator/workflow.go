package orchestrator

import (
	"context"
	"fmt"
	"html"
	"net"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	bkc "github.com/moby/buildkit/client"
	"go.temporal.io/sdk/client"

	"github.com/joshjon/conduit-ci/pkg/constants"
	"github.com/joshjon/conduit-ci/pkg/github"
	"github.com/joshjon/conduit-ci/pkg/log"
	"github.com/joshjon/conduit-ci/pkg/temporal"
	"github.com/joshjon/conduit-ci/sdk/conduit"
)

type Config struct {
	HostPort  string
	Namespace string
	Project   string
	Repo      github.RepoRef
}

type Orchestrator struct {
	cfg    Config
	logger log.Logger
}

func NewOrchestrator(logger log.Logger, cfg Config) *Orchestrator {
	return &Orchestrator{
		cfg:    cfg,
		logger: logger,
	}
}

func (o *Orchestrator) Run(ctx context.Context) error {
	tc, err := temporal.NewClient(ctx, o.logger, o.cfg.HostPort, o.cfg.Namespace)
	if err != nil {
		return err
	}
	defer tc.Close()

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

	_, port, err := net.SplitHostPort(o.cfg.HostPort)
	if err != nil {
		return err
	}

	envs := map[string]string{
		"CONDUIT_HOST_PORT": "host.docker.internal:" + port,
		"CONDUIT_NAMESPACE": o.cfg.Namespace,
		"CONDUIT_PROJECT":   o.cfg.Project,
	}

	cli, err := bkc.New(ctx, "tcp://127.0.0.1:1234")
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	resp, err := cli.ListWorkers(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Printf("workers: %+v\n", resp)

	runner, err := StartGoWithBuilder(ctx, "tcp://127.0.0.1:1234", repoPath, ".conduit", nil, envs, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer runner.Stop()

	wfOpts := client.StartWorkflowOptions{TaskQueue: conduit.GetTaskQueue(o.cfg.Project, constants.ComponentPipeline)}
	o.logger.Info("executing pipeline workflow", "workflow.name", constants.WorkflowPipeline, "task_queue", wfOpts.TaskQueue)

	wr, err := tc.ExecuteWorkflow(ctx, wfOpts, constants.WorkflowPipeline)
	if err != nil {
		return err
	}

	// TODO: query dependant modules and start the workers for them

	return wr.Get(ctx, nil)
}
