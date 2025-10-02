package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/joshjon/conduit-ci/orchestrator"
	"github.com/joshjon/conduit-ci/pkg/github"
	"github.com/joshjon/conduit-ci/pkg/log"
	"github.com/joshjon/conduit-ci/pkg/temporal"
)

// TODO: load these from env
const (
	namespace = "acme"
	project   = "github.com/joshjon/fuzzy-train"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()
	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	logger := log.NewLogger(log.WithDevelopment())

	srv, addr, uiAddr, err := temporal.StartDevServer(namespace)
	if err != nil {
		return err
	}
	defer srv.Stop()
	logger.Info("started temporal dev server", "frontend.host_port", addr, "ui.address", uiAddr)

	client, err := temporal.NewClient(ctx, logger, addr, namespace)
	if err != nil {
		return err
	}
	defer client.Close()

	o := orchestrator.NewOrchestrator(logger, client, orchestrator.Config{
		Namespace: namespace,
		Project:   project,
		Repo: github.RepoRef{
			Repo:   "joshjon/fuzzy-train",
			Ref:    "8d78f1c",
			Subdir: "",
			Token:  "",
		},
	})
	if err = o.Run(ctx); err != nil {
		return err
	}

	<-make(chan struct{}) // TODO: remove me

	return nil
}
