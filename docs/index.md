# gci

Deploy services to any Linux host over SSH, without building images.

- [Installation](/installation/)
- [Configuration (`gci.toml`)](/configuration)
- [Commands](/reference/commands)

## Architecture

```
            :80
             │
      ┌──────▼──────┐   file-based xDS (CDS/LDS), hot-reloaded
      │  gci-envoy  │
      └──┬───────┬──┘
   gci0 bridge (10.77.0.0/24)
  ┌──────▼──┐ ┌──▼─────────┐ ┌────────────┐
  │ web-rev │ │ postgres   │ │ clickhouse │   Incus system containers
  └─────────┘ └──┬─────────┘ └──┬─────────┘   /nix/store mounted read-only
               volume         volume           (Incus custom volumes)
```

**Bootstrap** (once per host) installs Incus and Nix, then creates the `gci0` bridge, the `gci` profile, a small Debian 12 base image (`gci-base`), and the Envoy container.

**Deploy** ships only changed sources, realises only missing Nix paths, and replaces only services whose revision changed. Stateless services roll with no downtime, and stateful services are stopped and then restarted on the same volume.

**Service discovery:** services reach each other through Envoy on stable per-service VIPs (`postgres.platform.internal:5432`). External traffic enters through Envoy using each service's `routes`.

Current scope is a single host. Multi-host networking over WireGuard is planned.
