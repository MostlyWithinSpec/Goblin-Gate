# Prometheus and Grafana

The box can serve Prometheus metrics at `https://<box>/metrics`, the same address and port as the web UI.
It's off until you turn it on.

## Turn it on

**System → General → Metrics**: tick **Enable /metrics**, then choose who may scrape:

- **Allow list**: one CIDR per line, e.g. `192.168.1.50/32` for your Prometheus server. Addresses in the list
  need nothing else.
- **API token**: any other address can scrape with a valid token (**System → API Tokens**; a read-only
  token is enough). Send it as `Authorization: Bearer <token>`.

Save and **commit**. Everyone else gets `403`; with metrics off, `/metrics` answers `404`.

## Prometheus scrape config

```yaml
scrape_configs:
  - job_name: goblin-gate
    scheme: https
    tls_config:
      insecure_skip_verify: true   # the box's certificate is self-signed
    # only if Prometheus is not in the allow list:
    authorization:
      credentials: gg_xxxxxxxx
    static_configs:
      - targets: ["192.168.1.1:443"]
```

Use the admin port you set (System → General) if it isn't 443.

## Grafana

Import [`docs/grafana/goblin-gate.json`](https://github.com/MostlyWithinSpec/Goblin-Gate/blob/main/docs/grafana/goblin-gate.json)
(**Dashboards → New → Import**) and pick your Prometheus data source. It has traffic per interface, policy hits,
gateway latency and loss, IPS alerts, and CPU and memory.

## What is exported

| Metric | Labels | Meaning |
|---|---|---|
| `goblin_info` | `version`, `hostname` | always 1 |
| `goblin_uptime_seconds` | | since boot |
| `goblin_cpu_usage_ratio` | | 0–1 |
| `goblin_memory_used_bytes`, `goblin_memory_total_bytes` | | |
| `goblin_sessions` | | tracked connections |
| `goblin_interface_receive_bytes_total`, `..._transmit_bytes_total` | `interface`, `device` | traffic counters |
| `goblin_interface_up` | `interface`, `device` | link up = 1 |
| `goblin_policy_hits_total`, `goblin_policy_bytes_total` | `id`, `name` | packets/bytes per policy; restart at 0 on each commit |
| `goblin_gateway_up` | `gateway` | multi-WAN gateway monitor |
| `goblin_gateway_rtt_seconds`, `goblin_gateway_loss_ratio` | `gateway` | latency, loss 0–1 |
| `goblin_feed_entries` | `feed`, `type` | entries in each threat feed |
| `goblin_feed_last_success_timestamp_seconds` | `feed` | last good download |
| `goblin_ips_alerts_total`, `goblin_ips_drops_total` | | since the service started |
| `goblin_temp_blocks` | | active temporary IP blocks |
| `goblin_dhcp_leases` | `interface` | active leases |
| `goblin_commits_total`, `goblin_config_revision` | | commits made; running revision |

Use `rate()` / `increase()` on the `_total` counters; Prometheus handles their restarts.
