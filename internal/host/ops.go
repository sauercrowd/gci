package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
)

func Status(ctx context.Context, w io.Writer, app string) error {
	st, err := loadState(app)
	if err != nil {
		return err
	}
	if len(st.Services) == 0 {
		fmt.Fprintf(w, "app %q has no deployed services\n", app)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATE\tUNIT\tADDRESS\tIP\tREV\tCONTAINER")
	for _, name := range sortedKeys(st.Services) {
		s := st.Services[name]
		state, unit := "missing", "-"
		if inst, _ := getInstance(ctx, s.Container); inst != nil {
			state = strings.ToLower(inst.Status)
			if inst.Status == "Running" {
				out, _ := incus(ctx, "exec", s.Container, "--", "systemctl", "is-active", unitName(name))
				unit = strings.TrimSpace(out)
			}
		}
		addr := "-"
		if s.Port != 0 {
			addr = fmt.Sprintf("%s:%d", internalName(app, name), s.Port)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, state, unit, addr, s.IP, s.Rev[:8], s.Container)
	}
	return tw.Flush()
}

func Logs(ctx context.Context, w io.Writer, app, svc string, lines int, follow bool) error {
	st, err := loadState(app)
	if err != nil {
		return err
	}
	s, ok := st.Services[svc]
	if !ok {
		return fmt.Errorf("service %q not deployed (have: %s)", svc, strings.Join(sortedKeys(st.Services), ", "))
	}
	args := []string{"exec", s.Container, "--", "journalctl", "-u", unitName(svc), "-n", fmt.Sprint(lines), "--no-pager", "-o", "short-iso"}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func Remove(ctx context.Context, w io.Writer, app string, volumes bool) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := loadState(app)
	if err != nil {
		return err
	}
	list, err := listInstances(ctx, "user.gci.app="+app)
	if err != nil {
		return err
	}
	for _, inst := range list {
		if inst.Config["user.gci.app"] == app {
			fmt.Fprintf(w, "deleting %s\n", inst.Name)
			if err := deleteInstance(ctx, inst.Name); err != nil {
				return fmt.Errorf("delete %s: %w", inst.Name, err)
			}
		}
	}
	if volumes {
		out, _ := incus(ctx, "storage", "volume", "list", storagePool, "--format", "csv", "-c", "n", "type=custom")
		for _, line := range strings.Split(out, "\n") {
			if v := strings.TrimSpace(line); strings.HasPrefix(v, "gci-"+app+"-") {
				fmt.Fprintf(w, "deleting volume %s\n", v)
				if _, err := incus(ctx, "storage", "volume", "delete", storagePool, v); err != nil {
					return err
				}
			}
		}
	} else {
		for _, s := range st.Services {
			for vol := range s.Volumes {
				fmt.Fprintf(w, "keeping volume %s (use --volumes to delete)\n", volumeName(app, vol))
			}
		}
	}
	if err := os.RemoveAll(appDir(app)); err != nil {
		return err
	}
	return writeEnvoyConfig(envoyListenPort)
}
