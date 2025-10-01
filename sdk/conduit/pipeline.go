package conduit

import (
	"context"
	"time"

	"go.temporal.io/sdk/converter"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	name "github.com/joshjon/conduit-ci/pkg/fname"
)

type PipelineDefinition func(ctx Context) error

type JobDefinition func(ctx context.Context) error

type ArgJobDefinition[A any] func(ctx context.Context, arg A) error

type ResultJobDefinition[R any] func(ctx context.Context) (R, error)

type ArgResultJobDefinition[A any, R any] func(ctx context.Context, arg A) (R, error)

func StartJob(ctx Context, def JobDefinition) Future {
	wctx := withDefaultActivityOptions(ctx.workflowContext(), ctx.nextJobExecutionID().ActivityID())
	return &future{
		base: workflow.ExecuteActivity(wctx, name.FuncName(def)),
	}
}

func StartArgJob[A any](ctx Context, def ArgJobDefinition[A], arg A) Future {
	wctx := withDefaultActivityOptions(ctx.workflowContext(), ctx.nextJobExecutionID().ActivityID())
	argPayload, err := converter.GetDefaultDataConverter().ToPayload(arg)
	if err != nil {
		return &future{err: err}
	}
	return &future{base: workflow.ExecuteActivity(wctx, name.FuncName(def), argPayload)}
}

func StartResultJob[R any](ctx Context, def ResultJobDefinition[R]) ResultFuture[R] {
	wctx := withDefaultActivityOptions(ctx.workflowContext(), ctx.nextJobExecutionID().ActivityID())
	return &resultFuture[R]{
		base: workflow.ExecuteActivity(wctx, name.FuncName(def)),
	}
}

func StartArgResultJob[A any, R any](ctx Context, def ArgResultJobDefinition[A, R], arg A) ResultFuture[R] {
	wctx := withDefaultActivityOptions(ctx.workflowContext(), ctx.nextJobExecutionID().ActivityID())
	argPayload, err := converter.GetDefaultDataConverter().ToPayload(arg)
	if err != nil {
		return &resultFuture[R]{err: err}
	}
	return &resultFuture[R]{
		base: workflow.ExecuteActivity(wctx, name.FuncName(def), argPayload),
	}
}

func withDefaultActivityOptions(ctx workflow.Context, activityID string) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:             activityID,
		ScheduleToCloseTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		RetryPolicy: &temporalsdk.RetryPolicy{
			MaximumAttempts: 1,
		},
	})
}
