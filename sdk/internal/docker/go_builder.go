package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Runner controls a running (or starting) container.
type Runner struct {
	ContainerID string

	cli     *client.Client
	hijack  *types.HijackedResponse
	logDone chan error

	// Only for background log copy convenience.
	stdout io.Writer
	stderr io.Writer
}

// StartGo builds and starts the Go program at srcPath inside a golang:latest container,
// then returns immediately with a Runner handle. Logs stream in the background if
// stdout/stderr are provided (nil disables streaming).
func StartGo(
	ctx context.Context,
	srcPath string,
	runArgs []string,
	extraEnv map[string]string,
	stdout, stderr io.Writer,
) (*Runner, error) {
	if srcPath == "" {
		return nil, errors.New("srcPath is required")
	}
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return nil, fmt.Errorf("resolve srcPath: %w", err)
	}
	if fi, err := os.Stat(absSrc); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("srcPath must be an existing directory: %s", absSrc)
	}

	// Give a sensible default timeout unless caller supplied one.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker daemon not reachable: %w", err)
	}

	const imageRef = "golang:latest"
	if err := ensureImage(ctx, cli, imageRef, stdout); err != nil {
		_ = cli.Close()
		return nil, err
	}

	// Persistent caches for speed.
	const buildVol = "go-build-cache"
	const modVol = "go-mod-cache"
	if err := ensureVolume(ctx, cli, buildVol); err != nil {
		_ = cli.Close()
		return nil, err
	}
	if err := ensureVolume(ctx, cli, modVol); err != nil {
		_ = cli.Close()
		return nil, err
	}

	script := fmt.Sprintf(`set -e
echo "go version:"; go version
echo "go mod tidy:"; go mod tidy || true
echo "go build:"; go build -o /tmp/app .
echo "run:"; /tmp/app %s
`, shellJoin(runArgs))

	env := make([]string, 0, len(extraEnv))
	for k, v := range extraEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	hostSrc := toDockerHostPath(absSrc)

	cfg := &container.Config{
		Image:      imageRef,
		WorkingDir: "/src",
		Env:        env,
		Entrypoint: strslice.StrSlice{"/bin/sh", "-lc"},
		Cmd:        []string{script},

		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: hostSrc, Target: "/src"},
			{Type: mount.TypeVolume, Source: buildVol, Target: "/root/.cache/go-build"},
			{Type: mount.TypeVolume, Source: modVol, Target: "/go/pkg/mod"},
		},
	}

	cont, err := cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("container create: %w", err)
	}

	// Attach before starting so we don't miss early logs.
	var hijack *types.HijackedResponse
	if stdout != nil || stderr != nil {
		hr, err := cli.ContainerAttach(ctx, cont.ID, container.AttachOptions{
			Stream: true, Stdout: true, Stderr: true, Logs: true,
		})
		if err != nil {
			_ = cli.ContainerRemove(context.Background(), cont.ID, container.RemoveOptions{Force: true})
			_ = cli.Close()
			return nil, fmt.Errorf("container attach: %w", err)
		}
		hijack = &hr
	}

	if err := cli.ContainerStart(ctx, cont.ID, container.StartOptions{}); err != nil {
		if hijack != nil {
			hijack.Close()
		}
		_ = cli.ContainerRemove(context.Background(), cont.ID, container.RemoveOptions{Force: true})
		_ = cli.Close()
		return nil, fmt.Errorf("container start: %w", err)
	}

	r := &Runner{
		ContainerID: cont.ID,
		cli:         cli,
		hijack:      hijack,
		logDone:     make(chan error, 1),
		stdout:      stdout,
		stderr:      stderr,
	}

	// Begin background log copy if requested.
	if r.hijack != nil {
		go func() {
			_, copyErr := stdcopy.StdCopy(stdout, stderr, r.hijack.Reader)
			r.logDone <- copyErr
			close(r.logDone)
		}()
	}

	return r, nil
}

// Stop asks Docker to stop the container, waiting up to timeout.
func (r *Runner) Stop(ctx context.Context, timeout time.Duration) error {
	secs := int(timeout / time.Second)
	return r.cli.ContainerStop(ctx, r.ContainerID, container.StopOptions{Timeout: &secs})
}

// Kill sends SIGKILL to the container (immediate).
func (r *Runner) Kill(ctx context.Context) error {
	signal := "KILL"
	return r.cli.ContainerKill(ctx, r.ContainerID, signal)
}

// Wait blocks until the container stops and returns its exit status code.
func (r *Runner) Wait(ctx context.Context) (int64, error) {
	statusCh, errCh := r.cli.ContainerWait(ctx, r.ContainerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return -1, fmt.Errorf("container wait: %w", err)
		}
	case st := <-statusCh:
		// Finish logs if we were streaming.
		if r.hijack != nil {
			r.hijack.Close()
			<-r.logDone // allow to drain; ignore copy error
		}
		return st.StatusCode, nil
	}
	return -1, nil
}

// Remove deletes the container. Call after Stop/Wait.
// Set force=true to remove even if still running (Docker will kill it).
func (r *Runner) Remove(ctx context.Context, force bool) error {
	return r.cli.ContainerRemove(ctx, r.ContainerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

// LogsTail fetches the last tailLines of container logs (stdout+stderr).
func (r *Runner) LogsTail(ctx context.Context, tailLines int) (string, error) {
	rc, err := r.cli.ContainerLogs(ctx, r.ContainerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: fmt.Sprintf("%d", tailLines),
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var b strings.Builder
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String(), sc.Err()
}

// Close closes internal resources (client & attach stream if still open).
// It does not stop/remove the container—call Stop/Remove explicitly.
func (r *Runner) Close() error {
	if r.hijack != nil {
		r.hijack.Close()
	}
	return r.cli.Close()
}

// ----------------- internals -----------------

func ensureImage(ctx context.Context, cli *client.Client, ref string, out io.Writer) error {
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull (%s): %w", ref, err)
	}
	defer rc.Close()
	if out != nil {
		sc := bufio.NewScanner(rc)
		count := 0
		for sc.Scan() {
			// throttle progress spam
			if count%20 == 0 {
				fmt.Fprintln(out, "pulling golang:latest …")
			}
			count++
		}
	}
	return nil
}

func ensureVolume(ctx context.Context, cli *client.Client, name string) error {
	_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: map[string]string{"created-by": "dockerrun"},
	})
	return err
}

func toDockerHostPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	return strings.ReplaceAll(p, `\`, `/`)
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`!&*()[]{};|<>?~") {
			quoted[i] = "'" + strings.ReplaceAll(a, `'`, `'\''`) + "'"
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}
