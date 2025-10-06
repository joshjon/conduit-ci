package conduit

import (
	"context"
	"fmt"
	"html"
	"os"

	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/joshjon/conduit-ci/pkg/constants"
	"github.com/joshjon/conduit-ci/pkg/fname"
	"github.com/joshjon/conduit-ci/pkg/log"
	"github.com/joshjon/conduit-ci/pkg/temporal"
)

func RegisterPipeline(w *Worker, def PipelineDefinition) {
	workflowFn := func(ctx workflow.Context) error {
		return def(newPipelineContext(ctx))
	}
	w.worker.RegisterWorkflowWithOptions(workflowFn, workflow.RegisterOptions{
		Name: constants.WorkflowPipeline,
	})
}

func RegisterJob(w *Worker, def JobDefinition) {
	w.worker.RegisterActivityWithOptions(def, activity.RegisterOptions{
		Name: fname.FuncName(def),
	})
}

func RegisterArgJob[A any](w *Worker, def ArgJobDefinition[A]) {
	activityFn := func(ctx context.Context, payload *common.Payload) error {
		arg, err := DecodeArg[A](payload)
		if err != nil {
			return fmt.Errorf("unmarshal job arg: %w", err)
		}
		return def(ctx, arg)
	}
	w.worker.RegisterActivityWithOptions(activityFn, activity.RegisterOptions{
		Name: fname.FuncName(def),
	})
}

func RegisterResultJob[R any](w *Worker, def ResultJobDefinition[R]) {
	w.worker.RegisterActivityWithOptions(def, activity.RegisterOptions{
		Name: fname.FuncName(def),
	})
}

func RegisterArgResultJob[A any, R any](w *Worker, def ArgResultJobDefinition[A, R]) {
	activityFn := func(ctx context.Context, payload *common.Payload) (R, error) {
		arg, err := DecodeArg[A](payload)
		if err != nil {
			return *new(R), fmt.Errorf("unmarshal job arg: %w", err)
		}
		return def(ctx, arg)
	}
	w.worker.RegisterActivityWithOptions(activityFn, activity.RegisterOptions{
		Name: fname.FuncName(def),
	})
}

type WorkerConfig struct {
	HostPort  string
	Namespace string
	Project   string
}

func loadWorkerConfig() (WorkerConfig, error) {
	cfg := WorkerConfig{}

	cfg.HostPort = os.Getenv("CONDUIT_HOST_PORT")
	if cfg.HostPort == "" {
		return WorkerConfig{}, fmt.Errorf("CONDUIT_HOST_PORT is not set")
	}

	cfg.Namespace = os.Getenv("CONDUIT_NAMESPACE")
	if cfg.Namespace == "" {
		return WorkerConfig{}, fmt.Errorf("CONDUIT_NAMESPACE is not set")
	}

	cfg.Project = os.Getenv("CONDUIT_PROJECT")
	if cfg.Project == "" {
		return WorkerConfig{}, fmt.Errorf("CONDUIT_PROJECT is not set")
	}

	return cfg, nil
}

type Worker struct {
	ctx    context.Context
	id     string
	client client.Client
	worker worker.Worker
}

func NewWorker() (*Worker, error) {
	ctx := context.Background()

	cfg, err := loadWorkerConfig()
	if err != nil {
		return nil, err
	}

	temporalClient, err := temporal.NewClient(ctx, log.NewLogger(), cfg.HostPort, cfg.Namespace)
	if err != nil {
		return nil, err
	}

	taskQueue := GetTaskQueue(cfg.Project, constants.ComponentPipeline)
	id := getRunnerIdentify(taskQueue)

	wrk := worker.New(temporalClient, taskQueue, worker.Options{
		DisableRegistrationAliasing: true,
		Identity:                    id,
	})

	return &Worker{
		ctx:    ctx,
		id:     id,
		client: temporalClient,
		worker: wrk,
	}, nil
}

func (w *Worker) Run() error {
	return w.worker.Run(worker.InterruptCh())
}

func GetTaskQueue(project string, component string) string {
	return fmt.Sprintf("%s#%s", html.EscapeString(project), html.EscapeString(component))
}

func getRunnerIdentify(taskQueueName string) string {
	return fmt.Sprintf("%d@%s@%s", os.Getpid(), getHostName(), taskQueueName)
}

func getHostName() string {
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "Unknown"
	}
	return hostName
}
