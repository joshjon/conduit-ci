package temporal

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/lmittmann/tint"
	"github.com/temporalio/cli/temporalcli/devserver"
)

const ip = "127.0.0.1"

func freePort() int {
	return devserver.MustGetFreePort(ip)
}

func StartDevServer(namespace string) (*devserver.Server, string, string, error) {
	opts := devserver.StartOptions{
		FrontendIP:             ip,
		FrontendPort:           freePort(),
		UIIP:                   ip,
		UIPort:                 freePort(),
		Namespaces:             []string{namespace},
		ClusterID:              uuid.NewString(),
		MasterClusterName:      "active",
		CurrentClusterName:     "active",
		InitialFailoverVersion: 1,
		Logger:                 slog.New(tint.NewHandler(os.Stdout, nil)),
		LogLevel:               slog.LevelError,
	}
	addr := fmt.Sprintf("%s:%d", ip, opts.FrontendPort)
	uiAddr := fmt.Sprintf("http://%s:%d", ip, opts.UIPort)
	srv, err := devserver.Start(opts)
	return srv, addr, uiAddr, err
}
