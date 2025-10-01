package conduit

import (
	"go.temporal.io/sdk/workflow"
)

type Future interface {
	IsReady() bool
	Wait(ctx Context) error
}

type ResultFuture[R any] interface {
	IsReady() bool
	Wait(ctx Context) (R, error)
}

type future struct {
	base workflow.Future
	err  error
}

func (p *future) IsReady() bool {
	return p.err != nil || p.base.IsReady()
}

func (p *future) Wait(ctx Context) error {
	if p.err != nil {
		return p.err
	}
	return p.base.Get(ctx.workflowContext(), nil)
}

type resultFuture[R any] struct {
	base workflow.Future
	err  error
}

func (p *resultFuture[R]) IsReady() bool {
	return p.err != nil || p.base.IsReady()
}

func (p *resultFuture[R]) Wait(ctx Context) (R, error) {
	if p.err != nil {
		return *new(R), p.err
	}

	var r R
	if err := p.base.Get(ctx.workflowContext(), &r); err != nil {
		return *new(R), err
	}
	return r, nil
}
