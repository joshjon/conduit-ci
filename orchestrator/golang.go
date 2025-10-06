package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshjon/conduit-ci/pkg/bkmini"
)

// StartGoWithBuilder builds your Go program and then runs it with logs,
// using only the bkmini abstraction (no Docker).
func StartGoWithBuilder(
	ctx context.Context,
	builderAddr string, // buildkit addr
	srcPath string, // repo root
	conduitPath string, // subdir for main module; can be ""
	runArgs []string, // args to /tmp/app
	env map[string]string,
	stdout, stderr io.Writer,
) (*bkmini.Runner, error) {
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(absSrc); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("srcPath must be a directory: %s", absSrc)
	}

	b, err := bkmini.New(ctx, builderAddr)
	if err != nil {
		return nil, err
	}

	b.SetDefaultPlatform("linux/arm64") // TODO: support other platforms

	source := b.Directory("src", absSrc)
	goBuild := b.CacheVolume("go-build-cache")
	goMod := b.CacheVolume("go-mod-cache")

	workdir := "/src"
	if conduitPath = strings.TrimPrefix(conduitPath, "/"); conduitPath != "" {
		workdir += "/" + conduitPath
	}

	// Build steps (cached):

	c := b.Container().
		From(ctx, "golang:1.25").
		WithDirectory("/src", source).
		WithMountedCache("/root/.cache/go-build", goBuild).
		WithMountedCache("/go/pkg/mod", goMod).
		WithWorkdir(workdir)

	for k, v := range env {
		c = c.WithEnv(k, v)
	}

	c = c.
		WithExec([]string{"go", "version"}). // quick sanity
		WithExec([]string{"go", "env"}).     // optional debug
		WithExec([]string{"go", "mod", "tidy"}).
		WithExec([]string{"go", "build", "-o", "/tmp/app", "."})

	return c.RunAndStream(ctx, append([]string{"/tmp/app"}, runArgs...), stdout, stderr)
}
