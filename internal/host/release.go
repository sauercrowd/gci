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
	"strconv"
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

func depsKey(baseFP, env, user, install, srcDir string, inputs []string) (string, error) {
	h := sha256.New()
	for _, value := range []string{baseFP, env, user, install} {
		hashString(h, value)
	}
	paths := append([]string(nil), inputs...)
	sort.Strings(paths)
	for _, input := range paths {
		if err := config.ValidateInstallInput(input); err != nil {
			return "", err
		}
		root := filepath.Join(srcDir, filepath.FromSlash(input))
		info, err := os.Lstat(root)
		if err != nil {
			return "", err
		}
		hashString(h, input)
		if !info.IsDir() {
			if err := hashPath(h, root, input, info); err != nil {
				return "", err
			}
			continue
		}
		var paths []string
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return "", err
		}
		sort.Strings(paths)
		for _, path := range paths {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return "", err
			}
			itemInfo, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			rel = filepath.ToSlash(rel)
			if rel == "." {
				rel = ""
			}
			hashString(h, rel)
			if err := hashPath(h, path, rel, itemInfo); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type hashWriter interface{ Write([]byte) (int, error) }

func hashString(h hashWriter, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = io.WriteString(h, value)
}

func hashPath(h hashWriter, path, rel string, info os.FileInfo) error {
	hashString(h, rel)
	hashString(h, strconv.FormatUint(uint64(info.Mode().Perm()), 8))
	switch {
	case info.Mode().IsRegular():
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return hashFileContents(h, f)
	case info.IsDir():
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		hashString(h, target)
	default:
		return fmt.Errorf("unsupported install input %q", path)
	}
	return nil
}

func hashFileContents(h hashWriter, r io.Reader) error {
	var buf [32 * 1024]byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(n))
			_, _ = h.Write(size[:])
			_, _ = h.Write(buf[:n])
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func releaseKey(baseFP, env, user, deps, sourceHash, build string, serviceEnv map[string]string) string {
	h := sha256.New()
	if deps != "" {
		hashString(h, deps)
	} else {
		hashString(h, baseFP)
		hashString(h, env)
		hashString(h, user)
	}
	hashString(h, sourceHash)
	hashString(h, build)
	for _, key := range sortedKeys(serviceEnv) {
		hashString(h, key)
		hashString(h, serviceEnv[key])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func releaseName(app, svc, key string) string {
	return fmt.Sprintf("gci-%s-%s-r%s", app, svc, key[:7])
}

func depsName(app, svc, key string) string {
	return fmt.Sprintf("gci-%s-%s-i%s", app, svc, key[:7])
}

func builderName(app, svc, key string) string {
	return fmt.Sprintf("gci-%s-%s-x%s", app, svc, key[:7])
}

func prepareRelease(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, name, env, baseFP, sourceHash string, st *AppState) (*Release, error) {
	svc := cfg.Services[name]
	srcDir := sourceDir(cfg.App, name, sourceHash)
	var depKey, dep string
	if svc.Install != "" {
		var err error
		depKey, err = depsKey(baseFP, env, svc.User, svc.Install, srcDir, svc.InstallInputs)
		if err != nil {
			return nil, fmt.Errorf("deps key: %w", err)
		}
		dep = depsName(cfg.App, name, depKey)
		if !exists(ctx, "info", dep) {
			if err := buildDeps(ctx, w, hc, cfg, name, env, sourceHash, depKey, srcDir); err != nil {
				return nil, err
			}
		} else {
			fmt.Fprintf(w, "==> %s: deps cached (%s)\n", name, dep)
		}
	}

	key := releaseKey(baseFP, env, svc.User, depKey, sourceHash, svc.Build, svc.Env)
	instanceName := releaseName(cfg.App, name, key)
	if !exists(ctx, "info", instanceName) {
		if err := buildRelease(ctx, w, hc, cfg, name, env, sourceHash, key, srcDir, dep); err != nil {
			return nil, err
		}
	} else {
		fmt.Fprintf(w, "==> %s: release cached (%s)\n", name, instanceName)
	}

	rel := &Release{Key: key, Instance: instanceName, Deps: dep, Env: env, Source: sourceHash, Created: time.Now().UTC()}
	for _, existing := range st.Releases[name] {
		if existing.Key == key {
			rel.Created = existing.Created
			break
		}
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

func buildDeps(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, svcName, env, sourceHash, key, srcDir string) error {
	svc := cfg.Services[svcName]
	builder := builderName(cfg.App, svcName, key)
	final := depsName(cfg.App, svcName, key)
	_ = deleteInstance(ctx, builder)
	args := []string{"init", baseImage, builder, "--profile", profileName, "-s", hc.StoragePool,
		"-c", "user.gci.app=" + cfg.App, "-c", "user.gci.service=" + svcName, "-c", "user.gci.role=build"}
	if _, err := incus(ctx, args...); err != nil {
		_ = deleteInstance(ctx, builder)
		return err
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
		fmt.Sprintf("chown %s:%s /var/cache/gci && mkdir -p /app && chown %s:%s /app", uid, gid, uid, gid)); err != nil {
		return fail(err)
	}
	if err := streamTarToInstance(ctx, w, builder, srcDir, svc.InstallInputs); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "exec", builder, "--", "chown", "-R", uid+":"+gid, "/app"); err != nil {
		return fail(err)
	}
	fmt.Fprintf(w, "==> %s: installing dependencies\n", svcName)
	if err := runBuildCommand(ctx, w, builder, uid, gid, baseBuildEnv(env, cfg.App, svcName, svc.User), svc.Install); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "config", "device", "remove", builder, "cache"); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "stop", builder); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "move", builder, final); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "config", "set", final, "user.gci.role", "deps"); err != nil {
		_ = deleteInstance(ctx, final)
		return err
	}
	if _, err := incus(ctx, "config", "set", final, "user.gci.key", key); err != nil {
		_ = deleteInstance(ctx, final)
		return err
	}
	return nil
}

func buildRelease(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, svcName, env, sourceHash, key, srcDir, deps string) error {
	svc := cfg.Services[svcName]
	builder := builderName(cfg.App, svcName, key)
	final := releaseName(cfg.App, svcName, key)
	_ = deleteInstance(ctx, builder)
	if deps != "" {
		if _, err := incus(ctx, "copy", deps, builder, "-s", hc.StoragePool,
			"-c", "user.gci.role=build"); err != nil {
			_ = deleteInstance(ctx, builder)
			return err
		}
		if _, err := incus(ctx, "config", "unset", builder, "user.gci.key"); err != nil {
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
	hasCache := svc.Build != ""
	cacheName := cacheVolumeName(cfg.App, svcName)
	if hasCache {
		if err := ensureCacheVolume(ctx, hc, cacheName); err != nil {
			return fail(err)
		}
		if _, err := incus(ctx, "config", "device", "add", builder, "cache", "disk",
			"pool="+hc.StoragePool, "source="+cacheName, "path=/var/cache/gci"); err != nil {
			return fail(err)
		}
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
	if hasCache {
		if _, err := incus(ctx, "exec", builder, "--", "sh", "-c",
			fmt.Sprintf("chown %s:%s /var/cache/gci && mkdir -p /app && chown %s:%s /app", uid, gid, uid, gid)); err != nil {
			return fail(err)
		}
	} else if _, err := incus(ctx, "exec", builder, "--", "mkdir", "-p", "/app"); err != nil {
		return fail(err)
	}
	if err := streamTarToInstance(ctx, w, builder, srcDir, nil); err != nil {
		return fail(err)
	}
	if _, err := incus(ctx, "exec", builder, "--", "chown", "-R", uid+":"+gid, "/app"); err != nil {
		return fail(err)
	}
	if svc.Build != "" {
		fmt.Fprintf(w, "==> %s: building source\n", svcName)
		buildEnv := baseBuildEnv(env, cfg.App, svcName, svc.User)
		for k, v := range svc.Env {
			buildEnv[k] = v
		}
		if err := runBuildCommand(ctx, w, builder, uid, gid, buildEnv, svc.Build); err != nil {
			return fail(err)
		}
	}
	if hasCache {
		if _, err := incus(ctx, "config", "device", "remove", builder, "cache"); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "stop", builder); err != nil {
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
	args := []string{"exec", container, "--user", uid, "--group", gid, "--cwd", "/app"}
	for _, key := range sortedKeys(vars) {
		args = append(args, "--env", key+"="+vars[key])
	}
	args = append(args, "--", "sh", "-c", command)
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

func streamTarToInstance(ctx context.Context, w io.Writer, container, root string, inputs []string) error {
	r, pipe := io.Pipe()
	tarErr := make(chan error, 1)
	go func() {
		tarErr <- writeTar(pipe, root, inputs)
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

func writeTar(dst io.Writer, root string, inputs []string) error {
	tw := tar.NewWriter(dst)
	var paths []string
	if inputs == nil {
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
	} else {
		set := map[string]bool{}
		add := func(path string) error {
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return filepath.WalkDir(path, func(p string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					set[p] = true
					return nil
				})
			}
			set[path] = true
			return nil
		}
		for _, input := range inputs {
			path := filepath.Join(root, filepath.FromSlash(input))
			if err := add(path); err != nil {
				return err
			}
			for parent := filepath.Dir(path); parent != root && strings.HasPrefix(parent, root+string(filepath.Separator)); parent = filepath.Dir(parent) {
				set[parent] = true
			}
		}
		for path := range set {
			paths = append(paths, path)
		}
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
