package host

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

// Plan is what the CLI sends to the host agent for a deploy.
type Plan struct {
	Config  *config.Config    `json:"config"`
	Sources map[string]string `json:"sources"` // service -> source content hash
}

// basePackages are added to every service env.
var basePackages = []string{"cacert", "coreutils", "bashInteractive"}

func unitName(svc string) string { return "gci-" + svc + ".service" }

func volumeName(app, vol string) string { return "gci-" + app + "-" + vol }

func profileLink(app, svc, suffix string) string {
	return filepath.Join(appDir(app), "profiles", svc+suffix)
}

// HasSource reports whether a source tree with this hash was already uploaded.
func HasSource(app, svc, hash string) bool {
	_, err := os.Stat(sourceDir(app, svc, hash))
	return err == nil
}

// ReceiveSource extracts a gzipped tarball into the content-addressed source dir.
func ReceiveSource(app, svc, hash string, r io.Reader) error {
	dst := sourceDir(app, svc, hash)
	if HasSource(app, svc, hash) {
		_, _ = io.Copy(io.Discard, r)
		return nil
	}
	tmp := dst + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var links [][2]string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, filepath.Clean("/"+hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o755|0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			resolved := filepath.Join(filepath.Dir(target), hdr.Linkname)
			if filepath.IsAbs(hdr.Linkname) || (resolved != tmp && !strings.HasPrefix(resolved, tmp+string(filepath.Separator))) {
				return fmt.Errorf("source: symlink %s escapes the source tree", hdr.Name)
			}
			links = append(links, [2]string{hdr.Linkname, target})
		}
	}
	// Create links last so no file or directory is ever written through one.
	for _, l := range links {
		if err := os.Symlink(l[0], l[1]); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

func preflight(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("agent must run as root")
	}
	if _, ok := lookPath("incus"); !ok {
		return fmt.Errorf("incus not installed; run `gci bootstrap`")
	}
	if inst, err := getInstance(ctx, envoyName); err != nil || inst == nil {
		return fmt.Errorf("host not bootstrapped (no %s); run `gci bootstrap`", envoyName)
	}
	return Firewall(ctx)
}

// Apply converges the host to the plan: unchanged services are left alone,
// changed ones get a new container that must pass its health check before
// traffic (Envoy + /etc/hosts) moves to it.
func Apply(ctx context.Context, w io.Writer, plan Plan) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()

	cfg := plan.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	st, err := loadState(cfg.App)
	if err != nil {
		return err
	}
	order, err := cfg.Order()
	if err != nil {
		return err
	}
	start := time.Now()
	var changed []string
	for _, name := range order {
		did, err := applyService(ctx, w, cfg, name, plan.Sources[name], st)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if did {
			changed = append(changed, name)
		}
	}

	for name, s := range st.Services {
		if _, ok := cfg.Services[name]; ok {
			continue
		}
		fmt.Fprintf(w, "==> %s: removed from config, deleting %s (volumes kept)\n", name, s.Container)
		_ = deleteInstance(ctx, s.Container)
		delete(st.Services, name)
		_ = os.Remove(profileLink(cfg.App, name, ""))
		_ = os.Remove(profileLink(cfg.App, name, ".prev"))
	}
	if err := st.save(); err != nil {
		return err
	}
	if err := refreshHosts(ctx, st); err != nil {
		return err
	}
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		return err
	}
	if err := deleteOrphans(ctx, st); err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintf(w, "no changes (%s)\n", time.Since(start).Round(time.Millisecond))
	} else {
		fmt.Fprintf(w, "deployed %s in %s\n", strings.Join(changed, ", "), time.Since(start).Round(time.Second))
	}
	return nil
}

func revision(cfg *config.Config, name, env, sourceHash string) string {
	svc := *cfg.Services[name]
	svc.Source = ""
	svc.Routes = nil // routing changes do not require a new container
	raw, _ := json.Marshal(struct {
		Svc    config.Service
		Env    string
		Source string
	}{svc, env, sourceHash})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func applyService(ctx context.Context, w io.Writer, cfg *config.Config, name, sourceHash string, st *AppState) (bool, error) {
	svc := cfg.Services[name]
	app := cfg.App
	logf := func(format string, a ...any) { fmt.Fprintf(w, "==> %s: "+format+"\n", append([]any{name}, a...)...) }

	if svc.Source != "" && (sourceHash == "" || !HasSource(app, name, sourceHash)) {
		return false, fmt.Errorf("source %s was not uploaded", sourceHash)
	}

	t := time.Now()
	nextLink := profileLink(app, name, ".next")
	env, err := buildEnv(ctx, cfg.Nixpkgs, "gci-"+app+"-"+name, append(append([]string{}, svc.Packages...), basePackages...), nextLink)
	if err != nil {
		return false, fmt.Errorf("nix: %w", err)
	}
	defer os.Remove(nextLink)
	logf("env %s (%s)", filepath.Base(env), time.Since(t).Round(time.Millisecond))

	rev := revision(cfg, name, env, sourceHash)
	cur := st.Services[name]
	if cur != nil && cur.Rev == rev {
		inst, _ := getInstance(ctx, cur.Container)
		if inst != nil && inst.Status == "Running" && unitActiveProbe(ctx, cur.Container, unitName(name))() == nil {
			if !routesEqual(cur.Routes, svc.Routes) {
				cur.Routes = svc.Routes
				logf("routes updated")
				return true, st.save()
			}
			logf("unchanged (%s)", cur.Container)
			return false, nil
		}
	}

	container := fmt.Sprintf("gci-%s-%s-%s", app, name, rev[:8])
	if cur != nil && cur.Container == container {
		_ = deleteInstance(ctx, container)
		cur = nil
	}
	_ = deleteInstance(ctx, container)
	logf("creating %s", container)
	args := []string{"init", baseImage, container, "--profile", profileName,
		"-c", "user.gci.app=" + app, "-c", "user.gci.service=" + name, "-c", "user.gci.rev=" + rev}
	if svc.CPU != "" {
		args = append(args, "-c", "limits.cpu="+svc.CPU)
	}
	if svc.Memory != "" {
		args = append(args, "-c", "limits.memory="+svc.Memory)
	}
	if _, err := incus(ctx, args...); err != nil {
		return false, err
	}
	fail := func(err error) (bool, error) {
		logs, _ := incus(ctx, "exec", container, "--", "journalctl", "-u", unitName(name), "-n", "30", "--no-pager")
		if logs != "" {
			fmt.Fprintf(w, "---- %s logs ----\n%s\n----\n", name, logs)
		}
		_ = deleteInstance(ctx, container)
		if cur != nil && svc.Stateful() {
			logf("restoring previous container %s", cur.Container)
			_, _ = incus(ctx, "start", cur.Container)
		}
		return false, err
	}

	if svc.Source != "" {
		if _, err := incus(ctx, "config", "device", "add", container, "source", "disk",
			"source="+sourceDir(app, name, sourceHash), "path=/app", "readonly=true"); err != nil {
			return fail(err)
		}
	}
	vols := sortedKeys(svc.Volumes)
	for _, vol := range vols {
		vn := volumeName(app, vol)
		if !exists(ctx, "storage", "volume", "show", storagePool, vn) {
			logf("creating volume %s", vn)
			if _, err := incus(ctx, "storage", "volume", "create", storagePool, vn); err != nil {
				return fail(err)
			}
		}
	}
	// Stateful services own their volumes exclusively: stop the old container first.
	if cur != nil && svc.Stateful() {
		logf("stopping %s", cur.Container)
		if _, err := incus(ctx, "stop", cur.Container, "--timeout", "60"); err != nil {
			if inst, _ := getInstance(ctx, cur.Container); inst != nil && inst.Status == "Running" {
				return fail(err)
			}
		}
	}
	for _, vol := range vols {
		if _, err := incus(ctx, "config", "device", "add", container, "vol-"+vol, "disk",
			"pool="+storagePool, "source="+volumeName(app, vol), "path="+svc.Volumes[vol]); err != nil {
			return fail(err)
		}
	}

	if _, err := incus(ctx, "start", container); err != nil {
		return fail(err)
	}
	ip, err := waitIPv4(ctx, container, 60*time.Second)
	if err != nil {
		return fail(err)
	}
	waitSystemd(ctx, container)

	envVars := map[string]string{
		"PATH":          env + "/bin:/usr/bin:/bin",
		"SSL_CERT_FILE": env + "/etc/ssl/certs/ca-bundle.crt",
		"GCI_APP":       app,
		"GCI_SERVICE":   name,
		"HOME":          "/home/gci",
	}
	if svc.User == "root" {
		envVars["HOME"] = "/root"
	}
	if svc.Port != 0 {
		envVars["PORT"] = fmt.Sprint(svc.Port)
	}
	for k, v := range svc.Env {
		envVars[k] = v
	}
	var pre []string
	for _, vol := range vols {
		pre = append(pre, fmt.Sprintf("+/bin/chown %s: %s", svc.User, svc.Volumes[vol]))
	}
	files := map[string]string{
		"/etc/gci/service.env": renderEnvFile(envVars),
		"/etc/gci/run.sh":      shellScript(svc.Command),
	}
	if svc.Setup != "" {
		files["/etc/gci/setup.sh"] = "#!/bin/sh\nset -e\n" + svc.Setup + "\n"
		pre = append(pre, "/bin/sh /etc/gci/setup.sh")
	}
	workdir := envVars["HOME"]
	if svc.Source != "" {
		workdir = "/app"
	}
	files["/etc/systemd/system/"+unitName(name)] = renderUnit(unitSpec{
		Description: fmt.Sprintf("gci %s/%s", app, name),
		User:        svc.User,
		EnvFile:     "/etc/gci/service.env",
		WorkingDir:  workdir,
		ExecPre:     pre,
		ExecStart:   "/bin/sh /etc/gci/run.sh",
	})
	files["/etc/hosts"] = hostsFile(st, map[string]string{name: ip}, app)
	for _, p := range sortedKeys(files) {
		mode := "0644"
		if p == "/etc/gci/service.env" {
			mode = "0600"
		}
		if err := pushFile(ctx, container, p, files[p], mode); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "exec", container, "--", "sh", "-c",
		"systemctl daemon-reload && systemctl enable --now "+unitName(name)); err != nil {
		return fail(err)
	}

	var p probe
	switch {
	case svc.Health != "" && svc.Port != 0:
		p = httpProbe(fmt.Sprintf("http://%s:%d%s", ip, svc.Port, svc.Health))
	case svc.Port != 0:
		p = tcpProbe(fmt.Sprintf("%s:%d", ip, svc.Port))
	default:
		p = unitActiveProbe(ctx, container, unitName(name))
	}
	logf("waiting for health (%s)", ip)
	if err := waitHealthy(ctx, container, unitName(name), p, time.Duration(svc.HealthTimeout)*time.Second); err != nil {
		return fail(err)
	}

	// Promote: GC-root the env, switch traffic, then retire the old container.
	if old, err := os.Readlink(profileLink(app, name, "")); err == nil {
		_ = addRoot(ctx, old, profileLink(app, name, ".prev"))
	}
	if err := addRoot(ctx, env, profileLink(app, name, "")); err != nil {
		return false, err
	}
	st.Services[name] = &ServiceState{
		Container: container, IP: ip, Rev: rev, Env: env,
		Port: svc.Port, Routes: svc.Routes, Volumes: svc.Volumes,
	}
	if err := st.save(); err != nil {
		return false, err
	}
	if err := refreshHosts(ctx, st); err != nil {
		return false, err
	}
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		return false, err
	}
	if cur != nil {
		if !svc.Stateful() && svc.Port != 0 {
			time.Sleep(2 * time.Second) // let Envoy pick up the new cluster and drain
		}
		logf("retiring %s", cur.Container)
		_ = deleteInstance(ctx, cur.Container)
	}
	logf("ready at %s (%s)", ip, time.Since(t).Round(time.Millisecond))
	return true, nil
}

func addRoot(ctx context.Context, storePath, link string) error {
	nix, err := nixBin()
	if err != nil {
		return err
	}
	_, err = run(ctx, nix, "--extra-experimental-features", "nix-command", "build", "--out-link", link, storePath)
	return err
}

func hostsFile(st *AppState, override map[string]string, app string) string {
	var b strings.Builder
	b.WriteString("127.0.0.1 localhost\n::1 localhost ip6-localhost\n")
	ips := map[string]string{}
	for name, s := range st.Services {
		ips[name] = s.IP
	}
	for k, v := range override {
		ips[k] = v
	}
	for _, name := range sortedKeys(ips) {
		if ips[name] != "" {
			fmt.Fprintf(&b, "%s %s %s.%s.gci\n", ips[name], name, name, app)
		}
	}
	return b.String()
}

// refreshHosts rewrites /etc/hosts in every container of the app so services
// resolve each other by name (e.g. "postgres").
func refreshHosts(ctx context.Context, st *AppState) error {
	content := hostsFile(st, nil, st.App)
	for _, name := range sortedKeys(st.Services) {
		if err := pushFile(ctx, st.Services[name].Container, "/etc/hosts", content, "0644"); err != nil {
			return err
		}
	}
	return nil
}

func deleteOrphans(ctx context.Context, st *AppState) error {
	list, err := listInstances(ctx, "user.gci.app="+st.App)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, s := range st.Services {
		keep[s.Container] = true
	}
	for _, inst := range list {
		if inst.Config["user.gci.app"] == st.App && !keep[inst.Name] {
			_ = deleteInstance(ctx, inst.Name)
		}
	}
	return nil
}

func routesEqual(a, b []config.Route) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
