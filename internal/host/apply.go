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
	if _, err := loadHostConfig(); err != nil {
		return err
	}
	if inst, err := getInstance(ctx, envoyName); err != nil || inst == nil {
		return fmt.Errorf("host not bootstrapped (no %s); run `gci bootstrap`", envoyName)
	}
	return Firewall(ctx)
}

// Apply converges the host to the plan: unchanged services are left alone,
// changed ones get a new container that must pass its health check before
// traffic (Envoy) moves to it.
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
	if st.Releases == nil {
		st.Releases = map[string][]*Release{}
	}
	order, err := cfg.Order()
	if err != nil {
		return err
	}
	start := time.Now()
	hc, err := loadHostConfig()
	if err != nil {
		return err
	}
	resolved := make(map[string]map[string]string, len(order))
	var missing []string
	for _, name := range order {
		svc := cfg.Services[name]
		if svc.Source != "" && (plan.Sources[name] == "" || !HasSource(cfg.App, name, plan.Sources[name])) {
			return fmt.Errorf("%s: source %s was not uploaded", name, plan.Sources[name])
		}
		values, err := resolveSecrets(cfg.App, svc)
		if err != nil {
			if strings.HasPrefix(err.Error(), "missing secrets:") {
				missing = append(missing, name+": "+strings.TrimPrefix(err.Error(), "missing secrets: "))
				continue
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		resolved[name] = values
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing secrets: %s", strings.Join(missing, "; "))
	}

	baseFP := ""
	for _, name := range order {
		if cfg.Services[name].Source != "" {
			baseFP, err = baseFingerprint(ctx)
			if err != nil {
				return err
			}
			break
		}
	}
	prepared := make(map[string]preparedService, len(order))
	for _, name := range order {
		svc := cfg.Services[name]
		t := time.Now()
		nextLink := profileLink(cfg.App, name, ".next")
		env, err := buildEnv(ctx, cfg.Nixpkgs, "gci-"+cfg.App+"-"+name,
			append(append([]string{}, svc.Packages...), basePackages...), nextLink)
		_ = os.Remove(nextLink)
		if err != nil {
			return fmt.Errorf("%s: nix: %w", name, err)
		}
		fmt.Fprintf(w, "==> %s: env %s (%s)\n", name, filepath.Base(env), time.Since(t).Round(time.Millisecond))
		item := preparedService{env: env, secrets: resolved[name]}
		if svc.Source != "" {
			item.release, err = prepareRelease(ctx, w, hc, cfg, name, env, baseFP, plan.Sources[name], st)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		prepared[name] = item
	}

	var changed []string
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		return err
	}
	for _, name := range order {
		did, err := applyService(ctx, w, hc, cfg, name, plan.Sources[name], prepared[name], st)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if did {
			changed = append(changed, name)
			if err := writeEnvoyConfig(envoyListenPort); err != nil {
				return err
			}
		}
	}

	for name, s := range st.Services {
		if _, ok := cfg.Services[name]; ok {
			continue
		}
		fmt.Fprintf(w, "==> %s: removed from config, deleting %s (volumes kept)\n", name, s.Container)
		_ = deleteInstance(ctx, s.Container)
		delete(st.Services, name)
		for _, rel := range st.Releases[name] {
			_ = deleteInstance(ctx, rel.Instance)
			_ = os.Remove(releaseRoot(cfg.App, name, rel.Key))
		}
		delete(st.Releases, name)
		_ = os.Remove(profileLink(cfg.App, name, ""))
		_ = os.Remove(profileLink(cfg.App, name, ".prev"))
	}
	for name := range st.Releases {
		if _, ok := cfg.Services[name]; !ok {
			delete(st.Releases, name)
		}
	}
	if err := pruneAppReleases(ctx, st, 3); err != nil {
		return err
	}
	if err := st.save(); err != nil {
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
	return revisionWithSecrets(cfg, name, env, sourceHash, "")
}

func revisionWithSecrets(cfg *config.Config, name, env, releaseKey, secretsHash string) string {
	svc := *cfg.Services[name]
	svc.Source = ""
	svc.Routes = nil // routing changes do not require a new container
	raw, _ := json.Marshal(struct {
		Svc     config.Service
		Env     string
		Release string
		Secrets string
		Net     string
	}{svc, env, releaseKey, secretsHash, "release"})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func applyService(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, name, sourceHash string, prepared preparedService, st *AppState) (bool, error) {
	svc := cfg.Services[name]
	app := cfg.App
	logf := func(format string, a ...any) { fmt.Fprintf(w, "==> %s: "+format+"\n", append([]any{name}, a...)...) }

	if svc.Source != "" && (sourceHash == "" || !HasSource(app, name, sourceHash)) {
		return false, fmt.Errorf("source %s was not uploaded", sourceHash)
	}

	t := time.Now()
	env := prepared.env
	releaseKey := ""
	if prepared.release != nil {
		releaseKey = prepared.release.Key
	}
	rev := revisionWithSecrets(cfg, name, env, releaseKey, secretDigest(prepared.secrets))
	cur := st.Services[name]
	if cur != nil && cur.Rev == rev {
		inst, _ := getInstance(ctx, cur.Container)
		if inst != nil && inst.Status == "Running" && unitActiveProbe(ctx, cur.Container, unitName(name))() == nil {
			vip, err := serviceVIP(cur, svc, st)
			if err != nil {
				return false, err
			}
			if !routesEqual(cur.Routes, svc.Routes) || cur.VIP != vip || cur.HTTP != (svc.Health != "") {
				cur.Routes, cur.VIP, cur.HTTP = svc.Routes, vip, svc.Health != ""
				logf("routing updated")
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
	var args []string
	if prepared.release != nil {
		args = []string{"copy", prepared.release.Instance, container, "-s", hc.StoragePool,
			"-c", "user.gci.role=service", "-c", "user.gci.rev=" + rev}
	} else {
		args = []string{"init", baseImage, container, "--profile", profileName, "-s", hc.StoragePool,
			"-c", "user.gci.app=" + app, "-c", "user.gci.service=" + name,
			"-c", "user.gci.role=service", "-c", "user.gci.rev=" + rev}
	}
	if svc.CPU != "" {
		args = append(args, "-c", "limits.cpu="+svc.CPU)
	}
	if svc.Memory != "" {
		args = append(args, "-c", "limits.memory="+svc.Memory)
	}
	if _, err := incus(ctx, args...); err != nil {
		return false, err
	}
	if prepared.release != nil {
		if _, err := incus(ctx, "config", "unset", container, "user.gci.key"); err != nil {
			_ = deleteInstance(ctx, container)
			return false, err
		}
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

	vols := sortedKeys(svc.Volumes)
	volPools := make(map[string]string, len(vols))
	for _, vol := range vols {
		vn := volumeName(app, vol)
		pool, err := volumePool(ctx, hc, vn)
		if err != nil {
			return fail(err)
		}
		volPools[vol] = pool
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
			"pool="+volPools[vol], "source="+volumeName(app, vol), "path="+svc.Volumes[vol]); err != nil {
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
	for k, v := range prepared.secrets {
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
	files["/etc/systemd/resolved.conf.d/gci.conf"] = "[Resolve]\nDomains=" + app + ".internal\n"
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
		"systemctl try-restart systemd-resolved; systemctl daemon-reload && systemctl enable --now "+unitName(name)); err != nil {
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
	vip, err := serviceVIP(cur, svc, st)
	if err != nil {
		return false, err
	}
	st.Services[name] = &ServiceState{
		Container: container, IP: ip, VIP: vip, HTTP: svc.Health != "", Rev: rev, Env: env, Release: releaseKey,
		Port: svc.Port, Routes: svc.Routes, Volumes: svc.Volumes,
	}
	if err := st.save(); err != nil {
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

func deleteOrphans(ctx context.Context, st *AppState) error {
	list, err := listInstances(ctx, "user.gci.app="+st.App)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, s := range st.Services {
		keep[s.Container] = true
	}
	for _, rels := range st.Releases {
		for _, rel := range rels {
			keep[rel.Instance] = true
			if rel.Deps != "" {
				keep[rel.Deps] = true
			}
		}
	}
	for _, inst := range list {
		if inst.Config["user.gci.app"] == st.App && !keep[inst.Name] {
			if err := deleteInstance(ctx, inst.Name); err != nil {
				return fmt.Errorf("delete orphan %s: %w", inst.Name, err)
			}
		}
	}
	return nil
}

func pruneAppReleases(ctx context.Context, st *AppState, keep int) error {
	for name, rels := range st.Releases {
		current := ""
		if service := st.Services[name]; service != nil {
			current = service.Release
		}
		kept, dropped := pruneReleases(rels, current, keep)
		for _, rel := range dropped {
			if err := deleteExistingInstance(ctx, rel.Instance); err != nil {
				return err
			}
			if err := removeRoot(releaseRoot(st.App, name, rel.Key)); err != nil {
				return err
			}
		}
		if len(kept) == 0 {
			delete(st.Releases, name)
		} else {
			st.Releases[name] = kept
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

// serviceVIP keeps a service's existing VIP or allocates one; services
// without a port get none.
func serviceVIP(cur *ServiceState, svc *config.Service, st *AppState) (string, error) {
	if svc.Port == 0 {
		return "", nil
	}
	if cur != nil && cur.VIP != "" {
		return cur.VIP, nil
	}
	hc, err := loadHostConfig()
	if err != nil {
		return "", err
	}
	return allocateVIP(hc, st)
}
