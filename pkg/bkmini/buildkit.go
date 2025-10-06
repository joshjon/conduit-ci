package bkmini

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	bkc "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Builder is the top-level wrapper over a BuildKit client.
type Builder struct {
	c         *bkc.Client
	localDirs map[string]string // logical name -> host path for llb.Local()
	platform  *specs.Platform   // default platform to use
}

// New returns a Builder that talks to a running buildkitd.
// addr examples: "unix:///run/buildkit/buildkitd.sock", "tcp://127.0.0.1:1234"
func New(ctx context.Context, addr string) (*Builder, error) {
	c, err := bkc.New(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &Builder{c: c, localDirs: map[string]string{}}, nil
}

// Helper to set explicit platform (e.g. "linux/arm64" or "linux/amd64")
func (b *Builder) SetDefaultPlatform(osArch string) {
	// osArch like "linux/arm64" or "linux/amd64"
	var os, arch string
	if i := strings.IndexByte(osArch, '/'); i > 0 {
		os, arch = osArch[:i], osArch[i+1:]
	}
	b.platform = &specs.Platform{OS: os, Architecture: arch}
}

// Helper to detect platform from the first worker
func (b *Builder) DetectDefaultPlatform(ctx context.Context) error {
	ws, err := b.c.ListWorkers(ctx)
	if err != nil {
		return err
	}
	if len(ws) == 0 || len(ws[0].Platforms) == 0 {
		return fmt.Errorf("no workers or platforms reported by buildkitd")
	}
	p := ws[0].Platforms[0]
	b.platform = &specs.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
	return nil
}

func (b *Builder) Close() error { return b.c.Close() }

// Directory registers a host dir that you can mount in containers.
type Directory struct {
	localName string
	hostPath  string
}

func (b *Builder) MountedDirectory(localName, hostPath string) *Directory {
	abs := hostPath
	if !filepath.IsAbs(hostPath) {
		abs = filepath.Clean("./" + hostPath)
	}
	b.localDirs[localName] = abs
	return &Directory{localName: localName, hostPath: abs}
}

// CacheVolume represents a named, shared/persistent cache directory.
type CacheVolume struct{ key string }

func (b *Builder) CacheVolume(key string) *CacheVolume { return &CacheVolume{key: key} }

// Container is a fluent builder on top of an LLB state.
type Container struct {
	b       *Builder
	state   llb.State
	workdir string
	env     map[string]string
	mounts  []mountSpec
	err     error
}

type mountSpec struct {
	dest       string
	srcLocal   *Directory
	srcCache   *CacheVolume
	readonly   bool
	sourcePath string // optional subpath for Local
}

func (b *Builder) Container() *Container {
	return &Container{
		b:       b,
		state:   llb.Scratch(),
		workdir: "/",
		env:     map[string]string{},
	}
}

// From resolves ref to a platform-specific digest (if b.platform set), sets base image,
// and automatically imports the image's Env (PATH, etc.). Any error is stored in c.err to
// keep the chain fluent; Solve/RunAndStream will surface it.
func (c *Container) From(ctx context.Context, ref string) *Container {
	c2 := *c

	// Choose image state (pinned if platform set)
	if c2.b.platform != nil {
		gp := v1.Platform{
			OS:           c2.b.platform.OS,
			Architecture: c2.b.platform.Architecture,
			Variant:      c2.b.platform.Variant,
		}
		dgst, err := resolveImageDigestForPlatform(ctx, ref, gp)
		if err != nil {
			c2.err = fmt.Errorf("resolve %s for platform %s/%s: %w", ref, gp.OS, gp.Architecture, err)
			c2.state = llb.Image(ref) // fall back so graph is still buildable
		} else {
			c2.state = llb.Image(ref + "@" + dgst)
		}
	} else {
		c2.state = llb.Image(ref)
	}

	// Auto-import image Env (don’t overwrite user-provided vars)
	if c2.err == nil {
		envs, err := fetchImageEnv(ctx, ref, c2.b.platform)
		if err != nil {
			// Non-fatal: keep chaining, report later
			c2.err = fmt.Errorf("load image env for %s: %w", ref, err)
		} else {
			if c2.env == nil {
				c2.env = map[string]string{}
			}
			for k, v := range envs {
				if _, exists := c2.env[k]; !exists {
					c2.env[k] = v
				}
			}
		}
	}

	return &c2
}

// WithDirectory mounts a registered Directory at dest.
func (c *Container) WithDirectory(dest string, d *Directory) *Container {
	c2 := *c
	c2.mounts = append(c2.mounts, mountSpec{dest: dest, srcLocal: d})
	return &c2
}

// WithMountedCache mounts a persistent cache (e.g., /root/.cache/go-build).
func (c *Container) WithMountedCache(dest string, cv *CacheVolume) *Container {
	c2 := *c
	c2.mounts = append(c2.mounts, mountSpec{dest: dest, srcCache: cv})
	return &c2
}

// Readonly marks a previously-added mount as read-only.
func (c *Container) Readonly(dest string) *Container {
	c2 := *c
	for i := range c2.mounts {
		if c2.mounts[i].dest == dest {
			c2.mounts[i].readonly = true
		}
	}
	return &c2
}

// WithWorkdir sets the working directory for subsequent execs.
func (c *Container) WithWorkdir(dir string) *Container {
	c2 := *c
	c2.workdir = dir
	return &c2
}

// WithEnv sets/overrides an environment variable for subsequent execs.
func (c *Container) WithEnv(k, v string) *Container {
	c2 := *c
	c2.env[k] = v
	return &c2
}

// WithExec appends an ExecOp to the chain and returns the new container state.
func (c *Container) WithExec(argv []string) *Container {
	if c.err != nil {
		c2 := *c
		return &c2
	}
	opts := make([]llb.RunOption, 0, len(c.mounts)+2)

	// Command
	if len(argv) == 1 {
		opts = append(opts, llb.Shlex(argv[0]))
	} else {
		opts = append(opts, llb.Args(argv))
	}

	// Env
	for k, v := range c.env {
		opts = append(opts, llb.AddEnv(k, v))
	}

	// Mounts
	for _, m := range c.mounts {
		switch {
		case m.srcLocal != nil:
			mopts := []llb.MountOption{}
			if m.readonly {
				mopts = append(mopts, llb.Readonly)
			}
			if m.sourcePath != "" {
				mopts = append(mopts, llb.SourcePath(m.sourcePath))
			}
			opts = append(opts, llb.AddMount(m.dest, llb.Local(m.srcLocal.localName), mopts...))

		case m.srcCache != nil:
			mopts := []llb.MountOption{
				llb.AsPersistentCacheDir(m.srcCache.key, llb.CacheMountShared),
			}
			if m.readonly {
				mopts = append(mopts, llb.Readonly)
			}
			opts = append(opts, llb.AddMount(m.dest, llb.Scratch(), mopts...))
		}
	}

	// Network (sandbox) and workdir
	opts = append(opts, llb.Network(llb.NetModeSandbox))

	run := c.state.Dir(c.workdir).Run(opts...)
	c2 := *c
	c2.state = run.Root()
	return &c2
}

func (c *Container) marshal(ctx context.Context) (*llb.Definition, error) {
	// Platform is pinned by FromPlatform via image digest; no marshal-time constraints needed.
	return c.state.Marshal(ctx)
}

// Export options --------------------------------------------------------------

type ExportKind int

const (
	ExportNone        ExportKind = iota // just execute & warm cache
	ExportDockerImage                   // export as Docker image (loads into Docker if supported)
	ExportOCI                           // export as OCI tar
)

type Export struct {
	Kind       ExportKind
	ImageName  string            // for ExportDockerImage
	OutputPath string            // for ExportOCI (defaults "image.oci.tar")
	Attrs      map[string]string // exporter-specific attrs
}

// Solve executes the current graph (and optionally exports an image).
func (c *Container) Solve(ctx context.Context, export Export) error {
	if c.err != nil {
		return c.err
	}
	def, err := c.marshal(ctx)
	if err != nil {
		return fmt.Errorf("marshal llb: %w", err)
	}

	var exports []bkc.ExportEntry
	switch export.Kind {
	case ExportDockerImage:
		attrs := map[string]string{"name": export.ImageName}
		for k, v := range export.Attrs {
			attrs[k] = v
		}
		exports = []bkc.ExportEntry{{Type: bkc.ExporterDocker, Attrs: attrs}}

	case ExportOCI:
		out := export.OutputPath
		if out == "" {
			out = "image.oci.tar"
		}
		exports = []bkc.ExportEntry{{
			Type: bkc.ExporterOCI,
			Output: func(map[string]string) (io.WriteCloser, error) {
				return os.Create(out)
			},
		}}

	case ExportNone:
		exports = nil
	}

	ldirs := map[string]string{}
	for _, m := range c.mounts {
		if m.srcLocal != nil {
			ldirs[m.srcLocal.localName] = c.b.localDirs[m.srcLocal.localName]
		}
	}

	_, err = c.b.c.Solve(ctx, def, bkc.SolveOpt{
		LocalDirs: ldirs,
		Exports:   exports,
	}, nil)
	return err
}

// SolveAndStream executes the current graph (no extra exec) and streams logs from all steps.
// Succeeds if all prior .WithExec steps succeed; returns error if any step fails.
func (c *Container) SolveAndStream(
	ctx context.Context,
	stdout, stderr io.Writer,
) error {
	if c.err != nil {
		return c.err
	}
	def, err := c.marshal(ctx)
	if err != nil {
		return fmt.Errorf("marshal llb: %w", err)
	}

	statusCh := make(chan *bkc.SolveStatus, 16)
	solveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Stream logs
	doneLogs := make(chan struct{})
	go func() {
		defer close(doneLogs)
		for st := range statusCh {
			for _, l := range st.Logs {
				switch l.Stream {
				case 1:
					if stdout != nil {
						_, _ = stdout.Write(l.Data)
					}
				case 2:
					if stderr != nil {
						_, _ = stderr.Write(l.Data)
					}
				default:
					if stderr != nil {
						_, _ = stderr.Write(l.Data)
					}
				}
			}
		}
	}()

	_, err = c.b.c.Solve(solveCtx, def, bkc.SolveOpt{
		LocalDirs: c.collectLocalDirs(),
	}, statusCh)

	// Wait for log copier to drain
	<-doneLogs
	return err
}

// -------------------------- Running with logs --------------------------------

// Runner represents a single in-flight BuildKit solve that includes a final ExecOp.
type Runner struct {
	done   chan error
	cancel context.CancelFunc
}

// Stop cancels the running ExecOp (analogous to killing a container).
func (r *Runner) Stop() { r.cancel() }

// Wait waits for completion or cancellation.
func (r *Runner) Wait() error { return <-r.done }

// RunAndStream takes the current container state, appends a final ExecOp `argv`,
// executes the graph, and streams stdout/stderr from all ExecOps.
// Return value lets you Stop() and Wait().
func (c *Container) RunAndStream(
	ctx context.Context,
	argv []string,
	stdout, stderr io.Writer,
) (*Runner, error) {
	if c.err != nil {
		return nil, c.err
	}
	// Append the run command
	runC := c.WithExec(argv)

	def, err := runC.marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("marshal llb: %w", err)
	}

	statusCh := make(chan *bkc.SolveStatus, 16) // BuildKit will close this
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	// Stream logs until BuildKit closes statusCh.
	go func() {
		for st := range statusCh {
			for _, l := range st.Logs {
				switch l.Stream {
				case 1:
					if stdout != nil {
						_, _ = stdout.Write(l.Data)
					}
				case 2:
					if stderr != nil {
						_, _ = stderr.Write(l.Data)
					}
				default:
					if stderr != nil {
						_, _ = stderr.Write(l.Data)
					}
				}
			}
			// Optional: handle warnings/progress if you want
			// for _, w := range st.Warnings { ... }
			// for _, v := range st.Vertexes { ... }
		}
	}()

	// Kick off the solve. Do NOT close statusCh here; BuildKit owns it.
	go func() {
		_, solveErr := c.b.c.Solve(runCtx, def, bkc.SolveOpt{
			LocalDirs: c.collectLocalDirs(),
		}, statusCh)
		// statusCh gets closed by Solve once it returns.
		done <- solveErr
		close(done)
	}()

	return &Runner{done: done, cancel: cancel}, nil
}

func (c *Container) collectLocalDirs() map[string]string {
	ldirs := map[string]string{}
	for _, m := range c.mounts {
		if m.srcLocal != nil {
			ldirs[m.srcLocal.localName] = c.b.localDirs[m.srcLocal.localName]
		}
	}
	return ldirs
}

// resolveImageDigestForPlatform resolves <ref> to the child manifest digest that matches p.
// Example return: "sha256:abcd...".
// resolveImageDigestForPlatform resolves <ref> to the child manifest digest for platform p.
// It tolerates variant differences commonly seen on arm/arm64 (e.g., "" vs "v8").
func resolveImageDigestForPlatform(ctx context.Context, ref string, p v1.Platform) (string, error) {
	r, err := name.ParseReference(ref) // e.g. "golang:latest"
	if err != nil {
		return "", err
	}

	// Helper: are variants compatible for this arch?
	variantsCompatible := func(imgVar, wantVar, arch string) bool {
		switch arch {
		case "arm64":
			// arm64/"" and arm64/"v8" are effectively the same for selection purposes
			return true
		case "arm":
			// be lenient: if either side omits variant, accept
			if imgVar == "" || wantVar == "" {
				return true
			}
			return imgVar == wantVar
		default:
			// for other arches, require exact match (or both empty)
			return imgVar == wantVar
		}
	}

	// Try as a multi-arch index first.
	if idx, err := remote.Index(r, remote.WithContext(ctx)); err == nil {
		im, err := idx.IndexManifest()
		if err != nil {
			return "", err
		}
		for _, m := range im.Manifests {
			if m.Platform == nil {
				continue
			}
			if m.Platform.OS != p.OS || m.Platform.Architecture != p.Architecture {
				continue
			}
			if !variantsCompatible(m.Platform.Variant, p.Variant, p.Architecture) {
				continue
			}
			return m.Digest.String(), nil
		}
		// If we didn’t find a matching child in the index, try fetching the
		// platform-specific image directly; some registries resolve it server-side.
		if img, err := remote.Image(r, remote.WithContext(ctx), remote.WithPlatform(p)); err == nil {
			if d, derr := img.Digest(); derr == nil {
				return d.String(), nil
			}
		}
		// Fall through to single-arch attempt below.
	}

	// If it wasn't an index, it might already be single-arch: fetch and return its digest.
	img, err := remote.Image(r, remote.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("fetch image: %w", err)
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
}

// fetchImageEnv loads Env from the image config for ref (optionally platform-pinned via p).
func fetchImageEnv(ctx context.Context, ref string, p *specs.Platform) (map[string]string, error) {
	// Resolve to platform-specific child if we have a platform.
	imgRef := ref
	if p != nil {
		gp := v1.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
		dgst, err := resolveImageDigestForPlatform(ctx, ref, gp)
		if err != nil {
			return nil, err
		}
		imgRef = ref + "@" + dgst
	}

	r, err := name.ParseReference(imgRef)
	if err != nil {
		return nil, fmt.Errorf("parse ref: %w", err)
	}
	img, err := remote.Image(r, remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	out := make(map[string]string, len(cfg.Config.Env))
	for _, e := range cfg.Config.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out, nil
}
