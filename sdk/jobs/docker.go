package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshjon/conduit-ci/pkg/bkmini"
)

type Mount struct {
	LocalName     string
	HostPath      string
	Dest          string
	Readonly      bool
	SourceSubpath string
}

type CacheMount struct {
	Key      string
	Dest     string
	Readonly bool
}

type ExportConfig struct {
	OCIPath    string
	DockerName string
	Attrs      map[string]string
}

type DockerConfig struct {
	Image    string
	Platform string
	Mounts   []Mount
	Caches   []CacheMount
	Workdir  string
	Env      map[string]string
	Steps    [][]string
	Run      []string
	Export   *ExportConfig
}

type DockerResult struct {
	Stdout            string
	Stderr            string
	ExportedOCIPath   string
	ExportedDockerTag string
}

func Docker(ctx context.Context, cfg DockerConfig) (DockerResult, error) {
	var res DockerResult

	if strings.TrimSpace(cfg.Image) == "" {
		return res, errors.New("image is required")
	}

	builderAddr := "tcp://host.docker.internal:1234" // TODO: make configurable
	b, err := bkmini.New(ctx, builderAddr)
	if err != nil {
		return res, fmt.Errorf("buildkit connect: %w", err)
	}
	defer b.Close()

	if cfg.Platform != "" {
		b.SetDefaultPlatform(cfg.Platform)
	} else {
		_ = b.DetectDefaultPlatform(ctx)
	}

	c := b.Container().From(ctx, cfg.Image)

	for _, m := range cfg.Mounts {
		if m.HostPath == "" {
			return res, fmt.Errorf("mount missing HostPath for dest %q", m.Dest)
		}
		abs := m.HostPath
		if !filepath.IsAbs(abs) {
			abs = filepath.Clean("./" + m.HostPath)
		}
		localName := m.LocalName
		if localName == "" {
			localName = strings.ReplaceAll(abs, string(filepath.Separator), "_")
			if localName == "" {
				localName = "src"
			}
		}
		dir := b.MountedDirectory(localName, abs)
		c = c.WithDirectory(m.Dest, dir)
		if m.Readonly {
			c = c.Readonly(m.Dest)
		}
		_ = m.SourceSubpath
	}

	for _, cm := range cfg.Caches {
		if cm.Key == "" || cm.Dest == "" {
			return res, fmt.Errorf("cache mount requires Key and Dest")
		}
		cv := b.CacheVolume(cm.Key)
		c = c.WithMountedCache(cm.Dest, cv)
		if cm.Readonly {
			c = c.Readonly(cm.Dest)
		}
	}

	if cfg.Workdir != "" {
		c = c.WithWorkdir(cfg.Workdir)
	}
	for k, v := range cfg.Env {
		c = c.WithEnv(k, v)
	}

	for _, step := range cfg.Steps {
		if len(step) == 0 {
			continue
		}
		c = c.WithExec(step)
	}

	if cfg.Export != nil {
		exp := bkmini.Export{}
		switch {
		case cfg.Export.OCIPath != "":
			exp.Kind = bkmini.ExportOCI
			exp.OutputPath = cfg.Export.OCIPath
		case cfg.Export.DockerName != "":
			exp.Kind = bkmini.ExportDockerImage
			exp.ImageName = cfg.Export.DockerName
		default:
			exp.Kind = bkmini.ExportNone
		}
		if cfg.Export.Attrs != nil {
			exp.Attrs = cfg.Export.Attrs
		}
		if err := c.Solve(ctx, exp); err != nil {
			return res, fmt.Errorf("export solve: %w", err)
		}
		res.ExportedOCIPath = cfg.Export.OCIPath
		res.ExportedDockerTag = cfg.Export.DockerName
	}

	// ---- Execute graph and capture logs ----
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	stdout := io.MultiWriter(os.Stdout, outBuf)
	stderr := io.MultiWriter(os.Stderr, errBuf)

	if len(cfg.Run) == 0 {
		// Distroless-safe: no extra exec; just solve and stream logs.
		if err := c.SolveAndStream(ctx, stdout, stderr); err != nil {
			res.Stdout = outBuf.String()
			res.Stderr = errBuf.String()
			return res, fmt.Errorf("steps failed: %w", err)
		}
		res.Stdout = outBuf.String()
		res.Stderr = errBuf.String()
		return res, nil
	}

	// Final run provided: use RunAndStream
	runner, err := c.RunAndStream(ctx, cfg.Run, stdout, stderr)
	if err != nil {
		res.Stdout = outBuf.String()
		res.Stderr = errBuf.String()
		return res, fmt.Errorf("start run: %w", err)
	}
	defer runner.Stop()

	if err = runner.Wait(); err != nil {
		res.Stdout = outBuf.String()
		res.Stderr = errBuf.String()
		return res, fmt.Errorf("run failed: %w", err)
	}

	res.Stdout = outBuf.String()
	res.Stderr = errBuf.String()
	return res, nil
}
