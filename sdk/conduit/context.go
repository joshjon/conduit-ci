package conduit

import "go.temporal.io/sdk/workflow"

type Context interface {
	WorkDir() string
	workflowContext() workflow.Context
	nextJobExecutionID() JobExecutionID
}

type PipelineContext struct {
	ctx    workflow.Context
	currID JobExecutionID
}

func newPipelineContext(ctx workflow.Context) *PipelineContext {
	return &PipelineContext{
		ctx:    ctx,
		currID: 0,
	}
}

func (c *PipelineContext) WorkDir() string {
	return "/src"
}

func (c *PipelineContext) workflowContext() workflow.Context {
	return c.ctx
}

func (c *PipelineContext) nextJobExecutionID() JobExecutionID {
	c.currID++
	return c.currID
}
