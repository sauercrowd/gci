package host

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

const buildTimeout = 30 * time.Minute

type preparedService struct {
	env          string
	secrets      map[string]string
	secretDigest string
	release      *Release
}

func baseFingerprint(ctx context.Context) (string, error) {
	raw, err := incus(ctx, "image", "list", "--format=json")
	if err != nil {
		return "", err
	}
	var images []struct {
		Fingerprint string `json:"fingerprint"`
		Aliases     []struct {
			Name string `json:"name"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(raw), &images); err != nil {
		return "", err
	}
	for _, image := range images {
		for _, alias := range image.Aliases {
			if alias.Name == baseImage {
				return image.Fingerprint, nil
			}
		}
	}
	return "", fmt.Errorf("image alias %q not found", baseImage)
}

type hashWriter interface{ Write([]byte) (int, error) }

func hashString(h hashWriter, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = io.WriteString(h, value)
}

func chainKey(baseFP, env, user, install, reuse, build string, serviceEnv map[string]string) string {
	h := sha256.New()
	for _, value := range []string{baseFP, env, user, install, reuse} {
		hashString(h, value)
	}
	if reuse == "build" {
		hashString(h, build)
		for _, key := range sortedKeys(serviceEnv) {
			hashString(h, key)
			hashString(h, serviceEnv[key])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func releaseKey(chain, sourceHash, build string, serviceEnv map[string]string) string {
	h := sha256.New()
	hashString(h, chain)
	hashString(h, sourceHash)
	hashString(h, build)
	for _, key := range sortedKeys(serviceEnv) {
		hashString(h, key)
		hashString(h, serviceEnv[key])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func buildStart(rels []*Release, key, reuse string, clean bool) (from string, fresh bool) {
	if clean || reuse == "nothing" {
		return "", true
	}
	var newest *Release
	for _, rel := range rels {
		if rel.ChainKey != key || (reuse == "dependencies" && !rel.Installed) {
			continue
		}
		if newest == nil || rel.Created.After(newest.Created) {
			newest = rel
		}
	}
	if newest == nil {
		return "", true
	}
	if reuse == "dependencies" {
		return newest.Instance + "/installed", false
	}
	if reuse == "build" {
		return newest.Instance, false
	}
	return "", true
}

func releaseName(app, svc, key string) string {
	return fmt.Sprintf("gci-%s-%s-r%s", app, svc, key[:7])
}

func builderName(app, svc, key string) string {
	return fmt.Sprintf("gci-%s-%s-x%s", app, svc, key[:7])
}

func prepareRelease(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, name, env, baseFP, sourceHash string, st *AppState, clean bool) (*Release, error) {
	svc := cfg.Services[name]
	srcDir := sourceDir(cfg.App, name, sourceHash)
	chain := chainKey(baseFP, env, svc.User, svc.Install, svc.Reuse, svc.Build, svc.Env)
	key := releaseKey(chain, sourceHash, svc.Build, svc.Env)
	instanceName := releaseName(cfg.App, name, key)
	var previous *Release
	for _, existing := range st.Releases[name] {
		if existing.Key == key {
			previous = existing
			break
		}
	}
	cached := exists(ctx, "info", instanceName) && !clean
	created := time.Now().UTC()
	installed := svc.Reuse == "dependencies"
	if cached {
		fmt.Fprintf(w, "==> %s: release cached (%s)\n", name, instanceName)
		if previous != nil {
			created = previous.Created
			installed = previous.Installed
		}
	} else {
		from, fresh := buildStart(st.Releases[name], chain, svc.Reuse, clean)
		if fresh {
			fmt.Fprintf(w, "==> %s: building from base image\n", name)
		} else {
			fmt.Fprintf(w, "==> %s: building from %s\n", name, from)
		}
		if err := buildRelease(ctx, w, hc, cfg, name, env, key, srcDir, from); err != nil {
			return nil, err
		}
		installed = svc.Reuse == "dependencies"
	}

	rel := &Release{Key: key, ChainKey: chain, Instance: instanceName, Installed: installed, Env: env, Source: sourceHash, Created: created}
	if !cached {
		rel.Created = time.Now().UTC()
	}
	rels := st.Releases[name][:0]
	for _, existing := range st.Releases[name] {
		if existing.Key != key {
			rels = append(rels, existing)
		}
	}
	st.Releases[name] = append(rels, rel)
	if err := addRoot(ctx, env, releaseRoot(cfg.App, name, key)); err != nil {
		return nil, err
	}
	if err := st.save(); err != nil {
		return nil, err
	}
	return rel, nil
}

func buildRelease(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, svcName, env, key, srcDir, from string) error {
	svc := cfg.Services[svcName]
	builder := builderName(cfg.App, svcName, key)
	final := releaseName(cfg.App, svcName, key)
	_ = deleteInstance(ctx, builder)
	if from != "" {
		if _, err := incus(ctx, "copy", from, builder, "-s", hc.StoragePool,
			"-c", "user.gci.role=build"); err != nil {
			_ = deleteInstance(ctx, builder)
			return err
		}
		if err := unsetInstanceConfig(ctx, builder, "user.gci.key"); err != nil {
			_ = deleteInstance(ctx, builder)
			return err
		}
	} else {
		if _, err := incus(ctx, "init", baseImage, builder, "--profile", profileName, "-s", hc.StoragePool,
			"-c", "user.gci.app="+cfg.App, "-c", "user.gci.service="+svcName, "-c", "user.gci.role=build"); err != nil {
			_ = deleteInstance(ctx, builder)
			return err
		}
	}
	fail := func(err error) error {
		_ = deleteInstance(ctx, builder)
		return err
	}
	cacheName := cacheVolumeName(cfg.App, svcName)
	if err := ensureCacheVolume(ctx, hc, cacheName); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "config", "device", "add", builder, "cache", "disk",
		"pool="+hc.StoragePool, "source="+cacheName, "path=/var/cache/gci"); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "start", builder); err != nil {
		return fail(err)
	}
	if _, err := waitIPv4(ctx, builder, 60*time.Second); err != nil {
		return fail(err)
	}
	waitSystemd(ctx, builder)
	uid, gid, err := builderIDs(ctx, builder, svc.User)
	if err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "exec", builder, "--", "sh", "-c",
		fmt.Sprintf("chown %s:%s /var/cache/gci && mkdir -p /app /var/lib/gci && chown %s:%s /app /var/lib/gci", uid, gid, uid, gid)); err != nil {
		return fail(err)
	}
	if err := syncReleaseSource(ctx, w, builder, srcDir); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "exec", builder, "--", "chown", "-R", uid+":"+gid, "/app"); err != nil {
		return fail(err)
	}
	buildEnv := baseBuildEnv(env, cfg.App, svcName, svc.User)
	if svc.Install != "" {
		fmt.Fprintf(w, "==> %s: installing dependencies\n", svcName)
		if err := runBuildCommand(ctx, w, builder, uid, gid, buildEnv, svc.Install); err != nil {
			return fail(err)
		}
	}
	if svc.Reuse == "dependencies" {
		if _, err := incus(ctx, "config", "device", "remove", builder, "cache"); err != nil {
			return fail(err)
		}
		if _, err := incus(ctx, "snapshot", "create", builder, "installed"); err != nil {
			return fail(err)
		}
		if _, err := incus(ctx, "config", "device", "add", builder, "cache", "disk",
			"pool="+hc.StoragePool, "source="+cacheName, "path=/var/cache/gci"); err != nil {
			return fail(err)
		}
	}
	if svc.Build != "" {
		fmt.Fprintf(w, "==> %s: building source\n", svcName)
		for k, v := range svc.Env {
			buildEnv[k] = v
		}
		if err := runBuildCommand(ctx, w, builder, uid, gid, buildEnv, svc.Build); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "config", "device", "remove", builder, "cache"); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "stop", builder); err != nil {
		return fail(err)
	}
	if err := deleteExistingInstance(ctx, final); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "move", builder, final); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "config", "set", final, "user.gci.role", "release"); err != nil {
		_ = deleteInstance(ctx, final)
		return err
	}
	if _, err := incus(ctx, "config", "set", final, "user.gci.key", key); err != nil {
		_ = deleteInstance(ctx, final)
		return err
	}
	return nil
}

func builderIDs(ctx context.Context, builder, user string) (string, string, error) {
	uid, err := incus(ctx, "exec", builder, "--", "id", "-u", user)
	if err != nil {
		return "", "", err
	}
	gid, err := incus(ctx, "exec", builder, "--", "id", "-g", user)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(uid), strings.TrimSpace(gid), nil
}

func baseBuildEnv(env, app, svc, user string) map[string]string {
	home := "/home/" + user
	if user == "root" {
		home = "/root"
	}
	return map[string]string{
		"PATH":                 env + "/bin:/usr/bin:/bin",
		"HOME":                 home,
		"SSL_CERT_FILE":        env + "/etc/ssl/certs/ca-bundle.crt",
		"NIX_SSL_CERT_FILE":    env + "/etc/ssl/certs/ca-bundle.crt",
		"GCI_APP":              app,
		"GCI_SERVICE":          svc,
		"XDG_CACHE_HOME":       "/var/cache/gci",
		"npm_config_cache":     "/var/cache/gci/npm",
		"npm_config_store_dir": "/var/cache/gci/pnpm",
		"YARN_CACHE_FOLDER":    "/var/cache/gci/yarn",
		"UV_CACHE_DIR":         "/var/cache/gci/uv",
		"UV_LINK_MODE":         "copy",
		"PIP_CACHE_DIR":        "/var/cache/gci/pip",
	}
}

func runBuildCommand(ctx context.Context, w io.Writer, container, uid, gid string, vars map[string]string, command string) error {
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	args := buildCommandArgs(container, uid, gid, vars, command)
	cmd := exec.CommandContext(buildCtx, "incus", args...)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		if buildCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("build command timed out after %s", buildTimeout)
		}
		return fmt.Errorf("build command: %w", err)
	}
	return nil
}

func buildCommandArgs(container, uid, gid string, vars map[string]string, command string) []string {
	args := []string{"exec", container, "--user", uid, "--group", gid, "--cwd", "/app"}
	for _, key := range sortedKeys(vars) {
		args = append(args, "--env", key+"="+vars[key])
	}
	return append(args, "--", "sh", "-e", "-c", command)
}

func ensureCacheVolume(ctx context.Context, hc *HostConfig, name string) error {
	if exists(ctx, "storage", "volume", "show", hc.StoragePool, name) {
		return nil
	}
	_, err := incus(ctx, "storage", "volume", "create", hc.StoragePool, name)
	return err
}

func cacheVolumeName(app, svc string) string { return "gcicache-" + app + "-" + svc }

func deleteExistingInstance(ctx context.Context, name string) error {
	inst, err := getInstance(ctx, name)
	if err != nil || inst == nil {
		return err
	}
	return deleteInstance(ctx, name)
}

func removeRoot(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

const sourceManifestPath = "/var/lib/gci/source-manifest.json"

func sourceManifest(root string) ([]string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(entries)
	return entries, err
}

func sourceManifestDiff(old, current []string) ([]string, error) {
	newPaths := make(map[string]bool, len(current))
	for _, path := range current {
		if err := validateSourcePath(path); err != nil {
			return nil, err
		}
		newPaths[path] = true
	}
	removed := make([]string, 0)
	seen := make(map[string]bool, len(old))
	for _, path := range old {
		if err := validateSourcePath(path); err != nil {
			return nil, err
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		if !newPaths[path] {
			removed = append(removed, path)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func validateSourcePath(value string) error {
	if value == "" || filepath.IsAbs(value) {
		return fmt.Errorf("unsafe source manifest path %q", value)
	}
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe source manifest path %q", value)
		}
	}
	return nil
}

func syncReleaseSource(ctx context.Context, w io.Writer, container, root string) error {
	var old []string
	out, err := incus(ctx, "exec", container, "--", "sh", "-c",
		"if [ -f "+sourceManifestPath+" ]; then printf yes; else printf no; fi")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "yes" {
		tmp, err := os.CreateTemp("", "gci-source-manifest-*")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)
		if _, err := incus(ctx, "file", "pull", container+sourceManifestPath, tmpPath); err != nil {
			return err
		}
		raw, err := os.ReadFile(tmpPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return fmt.Errorf("parse source manifest: %w", err)
		}
	}
	current, err := sourceManifest(root)
	if err != nil {
		return err
	}
	removed, err := sourceManifestDiff(old, current)
	if err != nil {
		return err
	}
	if len(removed) > 0 {
		script := sourceRemovalScript(removed)
		if err := pushFile(ctx, container, "/tmp/gci-source-remove.sh", script, "0700"); err != nil {
			return err
		}
		if _, err := incus(ctx, "exec", container, "--", "sh", "/tmp/gci-source-remove.sh"); err != nil {
			return err
		}
	}
	if err := streamTarToInstance(ctx, w, container, root); err != nil {
		return err
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	return pushFile(ctx, container, sourceManifestPath, string(raw), "0644")
}

func sourceRemovalScript(removed []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	b.WriteString(`safe_parent() {
  parent=${1%/*}
  [ "$parent" = "$1" ] && parent=.
  while [ "$parent" != "." ]; do
    [ -L "/app/$parent" ] && return 1
    case "$parent" in */*) parent=${parent%/*} ;; *) parent=. ;; esac
  done
  return 0
}
`)
	b.WriteString("set --")
	for _, path := range removed {
		b.WriteByte(' ')
		b.WriteString(shellQuote(path))
	}
	b.WriteString(`
for p do
  if safe_parent "$p"; then
    rm -f -- "/app/$p" 2>/dev/null || true
  fi
done
`)
	dirs := map[string]bool{}
	for _, path := range removed {
		for dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))); dir != "."; dir = filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir))) {
			dirs[dir] = true
		}
		dirs[path] = true
	}
	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool {
		di, dj := strings.Count(ordered[i], "/"), strings.Count(ordered[j], "/")
		if di != dj {
			return di > dj
		}
		return ordered[i] < ordered[j]
	})
	for _, dir := range ordered {
		fmt.Fprintf(&b, "if safe_parent %s && [ ! -L %s ]; then rmdir -- %s 2>/dev/null || true; fi\n",
			shellQuote(dir), shellQuote("/app/"+dir), shellQuote("/app/"+dir))
	}
	return b.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func streamTarToInstance(ctx context.Context, w io.Writer, container, root string) error {
	r, pipe := io.Pipe()
	tarErr := make(chan error, 1)
	go func() {
		tarErr <- writeTar(pipe, root)
		_ = pipe.Close()
	}()
	args := []string{"exec", container, "--", "tar", "-C", "/app", "-xf", "-"}
	err := runStreamInput(ctx, w, r, "incus", args...)
	_ = r.Close()
	if err != nil {
		return err
	}
	return <-tarErr
}

func writeTar(dst io.Writer, root string) error {
	tw := tar.NewWriter(dst)
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.Mode()&os.ModeSymlink != 0 {
			hdr.Linkname, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return tw.Close()
}

func runStreamInput(ctx context.Context, w io.Writer, stdin io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func pruneReleases(rels []*Release, current string, keep int) (kept, dropped []*Release) {
	if keep <= 0 {
		return nil, append([]*Release(nil), rels...)
	}
	selected := map[string]bool{}
	for _, rel := range rels {
		if rel.Key == current {
			selected[rel.Key] = true
			break
		}
	}
	for i := len(rels) - 1; i >= 0 && len(selected) < keep; i-- {
		selected[rels[i].Key] = true
	}
	for _, rel := range rels {
		if selected[rel.Key] {
			kept = append(kept, rel)
		} else {
			dropped = append(dropped, rel)
		}
	}
	return kept, dropped
}
