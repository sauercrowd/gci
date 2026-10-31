package host

import (
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

type Plan struct {
	Config *config.Config `json:"config"`
}

var basePackages = []string{"cacert", "coreutils", "bashInteractive"}

func unitName(svc string) string        { return "gci-" + svc + ".service" }
func volumeName(app, vol string) string { return "gci-" + app + "-" + vol }
func profileLink(app, svc, suffix string) string {
	return filepath.Join(appDir(app), "profiles", svc+suffix)
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
	if cfg == nil {
		return fmt.Errorf("missing config in deployment plan")
	}
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
	hc, err := loadHostConfig()
	if err != nil {
		return err
	}

	start := time.Now()
	envs := make(map[string]string, len(order))
	for _, name := range order {
		svc := cfg.Services[name]
		nextLink := profileLink(cfg.App, name, ".next")
		_ = os.Remove(nextLink)
		t := time.Now()
		env, err := buildEnv(ctx, cfg.Nixpkgs, "gci-"+cfg.App+"-"+name,
			append(append([]string(nil), svc.Packages...), basePackages...), nextLink)
		if err != nil {
			return fmt.Errorf("%s: nix: %w", name, err)
		}
		_ = os.Remove(nextLink)
		envs[name] = env
		fmt.Fprintf(w, "==> %s: env %s (%s)\n", name, filepath.Base(env), time.Since(t).Round(time.Millisecond))
	}

	var changed []string
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		return err
	}
	for _, name := range order {
		did, err := applyService(ctx, w, hc, cfg, name, envs[name], st)
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
	for name, svc := range st.Services {
		if _, ok := cfg.Services[name]; ok {
			continue
		}
		fmt.Fprintf(w, "==> %s: removed from config, deleting %s (volumes kept)\n", name, svc.Container)
		_ = deleteInstance(ctx, svc.Container)
		delete(st.Services, name)
		_ = os.Remove(profileLink(cfg.App, name, ""))
		_ = os.Remove(profileLink(cfg.App, name, ".prev"))
	}
	if err := st.save(); err != nil {
		return err
	}
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Fprintf(w, "no changes (%s)\n", time.Since(start).Round(time.Millisecond))
	} else {
		fmt.Fprintf(w, "deployed %s in %s\n", strings.Join(changed, ", "), time.Since(start).Round(time.Second))
	}
	return nil
}

func revision(cfg *config.Config, name, env string) string {
	svc := *cfg.Services[name]
	svc.Routes = nil
	raw, _ := json.Marshal(struct {
		Service config.Service
		Env     string
	}{svc, env})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func applyService(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, name, env string, st *AppState) (bool, error) {
	svc := cfg.Services[name]
	app := cfg.App
	logf := func(format string, a ...any) { fmt.Fprintf(w, "==> %s: "+format+"\n", append([]any{name}, a...)...) }
	t := time.Now()
	rev := revision(cfg, name, env)
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
	args := []string{"init", baseImage, container, "--profile", profileName, "-s", hc.StoragePool,
		"-c", "user.gci.app=" + app, "-c", "user.gci.service=" + name,
		"-c", "user.gci.role=service", "-c", "user.gci.rev=" + rev}
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

	vols := sortedKeys(svc.Volumes)
	volPools := make(map[string]string, len(vols))
	for _, vol := range vols {
		pool, err := volumePool(ctx, hc, volumeName(app, vol))
		if err != nil {
			return fail(err)
		}
		volPools[vol] = pool
	}
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

	envVars := serviceEnvVars(app, name, svc, env)
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
	files["/etc/systemd/system/"+unitName(name)] = renderUnit(unitSpec{
		Description: fmt.Sprintf("gci %s/%s", app, name), User: svc.User,
		EnvFile: "/etc/gci/service.env", WorkingDir: workdir, ExecPre: pre,
		ExecStart: "/bin/sh /etc/gci/run.sh",
	})
	files["/etc/systemd/resolved.conf.d/gci.conf"] = "[Resolve]\nDomains=" + app + ".internal\n"
	for _, path := range sortedKeys(files) {
		mode := "0644"
		if path == "/etc/gci/service.env" {
			mode = "0600"
		}
		if err := pushFile(ctx, container, path, files[path], mode); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "exec", container, "--", "sh", "-c",
		"systemctl try-restart systemd-resolved; systemctl daemon-reload"); err != nil {
		return fail(err)
	}
	for _, vol := range vols {
		if _, err := incus(ctx, "exec", container, "--", "/bin/chown", svc.User+":", svc.Volumes[vol]); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "exec", container, "--", "systemctl", "enable", "--now", unitName(name)); err != nil {
		return fail(err)
	}

	timeout := time.Duration(svc.HealthTimeout) * time.Second
	deadline := time.Now().Add(timeout)
	healthCtx, cancelHealth := context.WithDeadline(ctx, deadline)
	defer cancelHealth()
	var p probe
	switch {
	case svc.Health != "" && svc.Port != 0:
		p = httpProbe(fmt.Sprintf("http://%s:%d%s", ip, svc.Port, svc.Health))
	case svc.Port != 0:
		p = tcpProbe(fmt.Sprintf("%s:%d", ip, svc.Port))
	default:
		p = unitActiveProbe(healthCtx, container, unitName(name))
	}
	logf("waiting for health (%s)", ip)
	if err := waitHealthyUntil(healthCtx, container, unitName(name), p, deadline, timeout); err != nil {
		return fail(err)
	}

	if old, err := os.Readlink(profileLink(app, name, "")); err == nil {
		_ = addRoot(ctx, old, profileLink(app, name, ".prev"))
	}
	if err := addRoot(ctx, env, profileLink(app, name, "")); err != nil {
		return fail(err)
	}
	vip, err := serviceVIP(cur, svc, st)
	if err != nil {
		return fail(err)
	}
	oldState := cur
	st.Services[name] = &ServiceState{
		Container: container, IP: ip, VIP: vip, HTTP: svc.Health != "", Rev: rev, Env: env,
		Port: svc.Port, Routes: svc.Routes, Volumes: svc.Volumes,
	}
	if err := st.save(); err != nil {
		if oldState == nil {
			delete(st.Services, name)
		} else {
			st.Services[name] = oldState
		}
		return fail(err)
	}
	if err := writeEnvoyConfig(envoyListenPort); err != nil {
		if oldState == nil {
			delete(st.Services, name)
		} else {
			st.Services[name] = oldState
		}
		_ = st.save()
		_ = writeEnvoyConfig(envoyListenPort)
		return fail(err)
	}
	if cur != nil {
		if !svc.Stateful() && svc.Port != 0 {
			time.Sleep(2 * time.Second)
		}
		logf("retiring %s", cur.Container)
		_ = deleteInstance(ctx, cur.Container)
	}
	logf("ready at %s (%s)", ip, time.Since(t).Round(time.Millisecond))
	return true, nil
}

func serviceEnvVars(app, name string, svc *config.Service, env string) map[string]string {
	vars := map[string]string{
		"PATH": env + "/bin:/usr/bin:/bin", "SSL_CERT_FILE": env + "/etc/ssl/certs/ca-bundle.crt",
		"GCI_APP": app, "GCI_SERVICE": name, "HOME": "/home/gci",
	}
	if svc.User == "root" {
		vars["HOME"] = "/root"
	}
	if svc.Port != 0 {
		vars["PORT"] = fmt.Sprint(svc.Port)
	}
	for key, value := range svc.Env {
		vars[key] = value
	}
	return vars
}

func addRoot(ctx context.Context, storePath, link string) error {
	nix, err := nixBin()
	if err != nil {
		return err
	}
	_, err = run(ctx, nix, "--extra-experimental-features", "nix-command", "build", "--out-link", link, storePath)
	return err
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
