package host

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"
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
	if err := tw.Flush(); err != nil {
		return err
	}
	hosts := map[string]RouteTLSSettings{}
	for _, service := range st.Services {
		for _, route := range service.Routes {
			settings := tlsSettings(route)
			if settings.Mode != "off" {
				hosts[route.Host] = settings
			}
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	certificates := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(certificates, "\nCERTIFICATE\tMODE\tSOURCE\tISSUER\tEXPIRES")
	for _, host := range sortedKeys(hosts) {
		meta := CertificateMeta{}
		if cert, err := readStoredCertificate(host); err == nil {
			meta = cert.Meta
		}
		expires := "-"
		if !meta.NotAfter.IsZero() {
			expires = meta.NotAfter.Format(time.RFC3339)
		}
		fmt.Fprintf(certificates, "%s\t%s\t%s\t%s\t%s\n",
			host, hosts[host].Mode, fallback(meta.Source), fallback(meta.Issuer), expires)
	}
	return certificates.Flush()
}

func fallback(value string) string {
	if value == "" {
		return "-"
	}
	return value
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
		hc, err := loadHostConfig()
		if err != nil {
			return err
		}
		pools := []string{hc.StoragePool}
		if hc.StoragePool != legacyPool {
			pools = append(pools, legacyPool)
		}
		for _, pool := range pools {
			out, err := incus(ctx, "storage", "volume", "list", pool, "--format", "csv", "-c", "n", "type=custom")
			if err != nil {
				return err
			}
			for _, line := range strings.Split(out, "\n") {
				v := strings.TrimSpace(line)
				if strings.HasPrefix(v, "gci-"+app+"-") || strings.HasPrefix(v, "gcicache-"+app+"-") {
					fmt.Fprintf(w, "deleting volume %s (%s)\n", v, pool)
					if _, err := incus(ctx, "storage", "volume", "delete", pool, v); err != nil {
						return err
					}
				}
			}
		}
	} else {
		for _, s := range st.Services {
			for vol := range s.Volumes {
				fmt.Fprintf(w, "keeping volume %s (use --volumes to delete)\n", volumeName(app, vol))
			}
		}
		fmt.Fprintf(w, "keeping gci secrets (use `gci secret rm` to remove them)\n")
	}
	if err := os.RemoveAll(appDir(app)); err != nil {
		return err
	}
	return writeEnvoyConfig(ctx)
}
