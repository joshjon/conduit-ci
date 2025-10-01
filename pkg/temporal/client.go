package temporal

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/joshjon/conduit-ci/pkg/log"
)

func NewClient(ctx context.Context, logger log.Logger, hostPort string, namespace string) (client.Client, error) {
	c, err := client.NewLazyClient(client.Options{
		HostPort:      hostPort,
		Namespace:     namespace,
		DataConverter: converter.GetDefaultDataConverter(),
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
		},
		Logger: FromLogger(logger),
	})
	if err != nil {
		return nil, err
	}

	if err = checkNamespaceHealth(ctx, c, namespace); err != nil {
		return nil, err
	}

	return c, nil
}

func checkNamespaceHealth(ctx context.Context, c client.Client, namespace string) error {
	const retryInterval = time.Second
	const retryMaxAttempts = 30

	operation := func() error {
		if _, err := c.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
			return err
		}

		in := &workflowservice.DescribeNamespaceRequest{
			Namespace: namespace,
		}
		wfService := c.WorkflowService()
		if _, err := wfService.DescribeNamespace(ctx, in); err != nil {
			return err
		}

		_, err := wfService.ListOpenWorkflowExecutions(ctx, &workflowservice.ListOpenWorkflowExecutionsRequest{
			Namespace: namespace,
		})
		return err
	}

	bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(retryInterval), retryMaxAttempts)

	if err := backoff.Retry(operation, backoff.WithContext(bo, ctx)); err != nil {
		return fmt.Errorf("temporal namespace '%s' unhealthy: %w", namespace, err)
	}

	return nil
}
