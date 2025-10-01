package ctools

import (
	"context"

	"go.temporal.io/sdk/client"

	"github.com/joshjon/conduit-ci/pkg/temporal"
	"github.com/joshjon/conduit-ci/sdk/conduit"
	"github.com/joshjon/conduit-ci/sdk/internal/constants"
)

// ExecutePipelineWorkflow is a temporary helper to execute a pipeline workflow.
// This should be removed once the orchestrator is ready to be used.
func ExecutePipelineWorkflow(ctx context.Context, cfg conduit.WorkerConfig) error {
	logger := cfg.Logger
	c, err := temporal.NewClient(ctx, cfg.Logger, cfg.HostPort, cfg.Namespace)
	if err != nil {
		return err
	}
	defer c.Close()

	logger.Info("starting pipeline workflow")
	taskQueue := conduit.GetTaskQueue(cfg.Project, constants.ComponentPipeline)
	wr, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, constants.WorkflowPipeline)
	if err != nil {
		return err
	}

	logger.Info("waiting for pipeline workflow to complete")
	if err = wr.Get(ctx, nil); err != nil {
		return err
	}
	logger.Info("pipeline workflow completed")

	return nil
}
