package host

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

type JobExitError struct {
	Code int
}

func (e *JobExitError) Error() string {
	return fmt.Sprintf("job exited with status %d", e.Code)
}

type RunPlan struct {
	Config  *config.Config    `json:"config"`
	Sources map[string]string `json:"sources"`
	Job     string            `json:"job"`
	Clean   bool              `json:"clean,omitempty"`
}

type preparedJob struct {
	name         string
	job          *config.Job
	image        preparedImage
	secrets      map[string]string
	secretDigest string
	rev          string
	user         string
	hasSource    bool
}

type runningJob struct {
	container string
	unit      string
	lease     *jobLease
}

type jobLease struct {
	file *os.File
	path string
}

func stringSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

func jobVolumeOwner(cfg *config.Config, job *config.Job) string {
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		owns := true
		for volume := range job.Volumes {
			if _, ok := svc.Volumes[volume]; !ok {
				owns = false
				break
			}
		}
		if owns {
			return name
		}
	}
	return ""
}

func jobRevisionWithRelease(cfg *config.Config, name, env, releaseKey string, releaseCreated time.Time, secretsHash string) string {
	job := *cfg.Jobs[name]
	image := *cfg.Images[job.Image]
	image.Source = ""
	raw, _ := json.Marshal(struct {
		Job            config.Job
		Image          config.Image
		Env            string
		Release        string
		ReleaseCreated time.Time
		Secrets        string
		Kind           string
	}{job, image, env, releaseKey, releaseCreated, secretsHash, "job"})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func acquireJobLease(container string) (*jobLease, error) {
	dir := filepath.Join(stateRoot, "jobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, container+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return &jobLease{file: file, path: path}, nil
}

func (l *jobLease) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	if removeErr := os.Remove(l.path); err == nil && !os.IsNotExist(removeErr) {
		err = removeErr
	}
	l.file = nil
	return err
}

func jobLeaseHeld(container string) (bool, error) {
	path := filepath.Join(stateRoot, "jobs", container+".lock")
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	return false, err
}

func jobEnvVars(app, name string, job *config.Job, image *config.Image, env string, secrets map[string]string) map[string]string {
	home := "/home/" + image.User
	if image.User == "root" {
		home = "/root"
	}
	vars := map[string]string{
		"PATH":          env + "/bin:/usr/bin:/bin",
		"SSL_CERT_FILE": env + "/etc/ssl/certs/ca-bundle.crt",
		"GCI_APP":       app,
		"GCI_JOB":       name,
		"HOME":          home,
	}
	for key, value := range image.Env {
		vars[key] = value
	}
	for key, value := range job.Env {
		vars[key] = value
	}
	for key, value := range secrets {
		vars[key] = value
	}
	return vars
}

func startJob(ctx context.Context, hc *HostConfig, cfg *config.Config, job preparedJob) (*runningJob, error) {
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	suffix := hex.EncodeToString(random[:])
	container := fmt.Sprintf("gci-%s-%s-j%s", cfg.App, job.name, suffix)
	unit := "gci-job-" + suffix
	lease, err := acquireJobLease(container)
	if err != nil {
		return nil, err
	}
	running := &runningJob{container: container, unit: unit, lease: lease}
	fail := func(err error) (*runningJob, error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = deleteExistingInstance(cleanupCtx, container)
		_ = lease.release()
		return nil, err
	}
	if err := deleteExistingInstance(ctx, container); err != nil {
		return fail(err)
	}
	var args []string
	if job.image.release != nil {
		args = []string{"copy", job.image.release.Instance, container, "-s", hc.StoragePool,
			"-c", "user.gci.app=" + cfg.App, "-c", "user.gci.image=" + job.job.Image,
			"-c", "user.gci.role=job", "-c", "user.gci.job=" + job.name}
	} else {
		args = []string{"init", baseImage, container, "--profile", profileName, "-s", hc.StoragePool,
			"-c", "user.gci.app=" + cfg.App, "-c", "user.gci.image=" + job.job.Image,
			"-c", "user.gci.role=job", "-c", "user.gci.job=" + job.name}
	}
	if job.job.CPU != "" {
		args = append(args, "-c", "limits.cpu="+job.job.CPU)
	}
	if job.job.Memory != "" {
		args = append(args, "-c", "limits.memory="+job.job.Memory)
	}
	if _, err := incus(ctx, args...); err != nil {
		return fail(err)
	}
	if job.image.release != nil {
		if err := unsetInstanceConfig(ctx, container, "user.gci.key"); err != nil {
			return fail(err)
		}
	}
	for _, volume := range sortedKeys(job.job.Volumes) {
		pool, err := volumePool(ctx, hc, volumeName(cfg.App, volume))
		if err != nil {
			return fail(err)
		}
		if _, err := incus(ctx, "config", "device", "add", container, "vol-"+volume, "disk",
			"pool="+pool, "source="+volumeName(cfg.App, volume), "path="+job.job.Volumes[volume]); err != nil {
			return fail(err)
		}
	}
	if _, err := incus(ctx, "start", container); err != nil {
		return fail(err)
	}
	if _, err := waitIPv4(ctx, container, 60*time.Second); err != nil {
		return fail(err)
	}
	waitSystemd(ctx, container)
	if _, err := incus(ctx, "exec", container, "--", "mkdir", "-p", "/app"); err != nil {
		return fail(err)
	}
	if err := pushFile(ctx, container, "/etc/systemd/resolved.conf.d/gci.conf",
		"[Resolve]\nDomains="+cfg.App+".internal\n", "0644"); err != nil {
		return fail(err)
	}
	_, _ = incus(ctx, "exec", container, "--", "systemctl", "try-restart", "systemd-resolved")
	envVars := jobEnvVars(cfg.App, job.name, job.job, cfg.Images[job.job.Image], job.image.env, job.secrets)
	if err := pushFile(ctx, container, "/etc/gci/service.env", renderEnvFile(envVars), "0600"); err != nil {
		return fail(err)
	}
	for _, volume := range sortedKeys(job.job.Volumes) {
		if _, err := incus(ctx, "exec", container, "--", "/bin/chown",
			cfg.Images[job.job.Image].User+":", job.job.Volumes[volume]); err != nil {
			return fail(err)
		}
	}
	return running, nil
}

func executeJob(ctx context.Context, w io.Writer, job preparedJob, running *runningJob) error {
	timeout := time.Duration(job.job.Timeout) * time.Second
	jobCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := jobCommandArgs(running.container, running.unit, job.user, job.hasSource, timeout, job.job.Run)
	output := &secretMaskWriter{dst: w, secrets: job.secrets}
	cmd := exec.CommandContext(jobCtx, "incus", args...)
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	_ = output.Flush()
	ctxErr := jobCtx.Err()
	if err == nil {
		return nil
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = incus(stopCtx, "exec", running.container, "--", "systemctl", "stop", running.unit)
	stopCancel()
	if ctxErr == context.DeadlineExceeded {
		return fmt.Errorf("job timed out after %d seconds", job.job.Timeout)
	}
	if ctxErr != nil {
		return ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &JobExitError{Code: exitErr.ExitCode()}
	}
	return fmt.Errorf("job command: %w", err)
}

func jobCommandArgs(container, unit, user string, hasSource bool, timeout time.Duration, command string) []string {
	workdir := "/home/" + user
	if user == "root" {
		workdir = "/root"
	}
	if hasSource {
		workdir = "/app"
	}
	return []string{"exec", container, "--", "systemd-run", "--unit", unit, "--wait", "--pipe", "--quiet", "--collect",
		"-p", fmt.Sprintf("RuntimeMaxSec=%d", int(timeout.Seconds())),
		"-p", "User=" + user, "-p", "EnvironmentFile=/etc/gci/service.env", "-p", "WorkingDirectory=" + workdir,
		"/bin/sh", "-e", "-c", command}
}

func runJob(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config, job preparedJob) error {
	running, err := startJob(ctx, hc, cfg, job)
	if err != nil {
		return err
	}
	defer cleanupJob(running)
	return executeJob(ctx, w, job, running)
}

func cleanupJob(running *runningJob) {
	if running == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = deleteExistingInstance(ctx, running.container)
	_ = running.lease.release()
}

type secretMaskWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	secrets map[string]string
	buffer  []byte
}

func (w *secretMaskWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, p...)
	for {
		index := strings.IndexByte(string(w.buffer), '\n')
		if index < 0 {
			break
		}
		line := w.buffer[:index+1]
		if _, err := io.WriteString(w.dst, maskJobSecrets(string(line), w.secrets)); err != nil {
			return 0, err
		}
		w.buffer = w.buffer[index+1:]
	}
	return len(p), nil
}

func (w *secretMaskWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buffer) == 0 {
		return nil
	}
	_, err := io.WriteString(w.dst, maskJobSecrets(string(w.buffer), w.secrets))
	w.buffer = nil
	return err
}

func maskJobSecrets(output string, secrets map[string]string) string {
	mask := make([]bool, len(output))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		searchFrom := 0
		for searchFrom < len(output) {
			offset := strings.Index(output[searchFrom:], secret)
			if offset < 0 {
				break
			}
			start := searchFrom + offset
			for i := start; i < start+len(secret); i++ {
				mask[i] = true
			}
			searchFrom = start + 1
		}
	}
	var sanitized strings.Builder
	for i := 0; i < len(output); {
		if mask[i] {
			sanitized.WriteString("[REDACTED]")
			for i < len(mask) && mask[i] {
				i++
			}
			continue
		}
		sanitized.WriteByte(output[i])
		i++
	}
	return sanitized.String()
}

func RunJob(ctx context.Context, w io.Writer, plan RunPlan) error {
	if plan.Config == nil {
		return fmt.Errorf("run plan has no config")
	}
	if err := preflight(ctx); err != nil {
		return err
	}
	cfg := plan.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	job, ok := cfg.Jobs[plan.Job]
	if !ok {
		return fmt.Errorf("unknown job %q", plan.Job)
	}
	if owner := jobVolumeOwner(cfg, job); len(job.Volumes) != 0 {
		return fmt.Errorf("job %q uses volumes of service %q; it only runs during %s's rollout", plan.Job, owner, owner)
	}
	image := cfg.Images[job.Image]
	if image.Source != "" && (plan.Sources[job.Image] == "" || !HasSource(cfg.App, job.Image, plan.Sources[job.Image])) {
		return fmt.Errorf("%s: source %s was not uploaded", job.Image, plan.Sources[job.Image])
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	st, err := loadState(cfg.App)
	if err != nil {
		return err
	}
	if st.Releases == nil {
		st.Releases = map[string][]*Release{}
	}
	if err := deleteOrphans(ctx, st); err != nil {
		return err
	}
	secrets, err := resolveJobSecrets(cfg.App, job)
	if err != nil {
		return err
	}
	digest, err := secretDigest(secrets)
	if err != nil {
		return err
	}
	hc, err := loadHostConfig()
	if err != nil {
		return err
	}
	baseFP := ""
	if image.Source != "" || len(image.Steps) > 0 {
		baseFP, err = baseFingerprint(ctx)
		if err != nil {
			return err
		}
	}
	link := profileLink(cfg.App, job.Image, ".next")
	env, err := buildEnv(ctx, cfg.Nixpkgs, "gci-"+cfg.App+"-"+job.Image,
		append(append([]string{}, image.Packages...), basePackages...), link)
	_ = os.Remove(link)
	if err != nil {
		return err
	}
	prepared := preparedImage{env: env}
	if image.Source != "" || len(image.Steps) > 0 {
		prepared.release, err = prepareRelease(ctx, w, hc, cfg, job.Image, env, baseFP, plan.Sources[job.Image], st, plan.Clean)
		if err != nil {
			return err
		}
	}
	if err := pruneAppReleases(ctx, st, 3); err != nil {
		return err
	}
	if err := st.save(); err != nil {
		return err
	}
	releaseKey := ""
	var releaseCreated time.Time
	if prepared.release != nil {
		releaseKey, releaseCreated = prepared.release.Key, prepared.release.Created
	}
	preparedRun := preparedJob{
		name: plan.Job, job: job, image: prepared, secrets: secrets, secretDigest: digest,
		rev:  jobRevisionWithRelease(cfg, plan.Job, env, releaseKey, releaseCreated, digest),
		user: image.User, hasSource: image.Source != "",
	}
	fmt.Fprintf(w, "==> %s: running\n", plan.Job)
	running, err := startJob(ctx, hc, cfg, preparedRun)
	if err != nil {
		return err
	}
	unlock()
	locked = false
	err = executeJob(ctx, w, preparedRun, running)
	cleanupJob(running)
	return err
}
