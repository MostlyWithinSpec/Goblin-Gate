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

## Flow export

Prometheus keeps rates and totals, not per-connection history. For long-term traffic history — who talked to
whom, for how long, how much — send the box's connections to a flow collector as **NetFlow v9** or **IPFIX**
over UDP. Nothing extra runs on the box: no ulogd, no kernel module.

**Log & Report → Flow export** (or the `flow_export` section):

| Field | Meaning |
|---|---|
| `enabled` | off by default |
| `collector` | the collector's `host:port`, e.g. `10.0.0.5:2055`. A DNS name is looked up at start and every 5 minutes. |
| `protocol` | `ipfix` (default) or `netflow9` |
| `active_timeout_seconds` | default 60, 10–600: a connection still alive after this gets a record every timeout with the counters since the last one, so a long download shows up while it runs |

Templates are resent every 20 packets and every 60 s, so a collector that restarts mid-night gets them back.
Every packet that carries data also carries the templates while a resend is due, because collectors decode
packets on several workers and a data packet can otherwise overtake its template.
`GET /api/v1/flow-export` shows what has been sent (`packets_sent`, `records_sent`, `last_error`, `last_sent`).
While the collector cannot be reached, up to 100,000 records wait for it; beyond that the oldest are dropped
and counted in `records_dropped`.

### The sampling limit

The records come from the connection tracking table, which the box reads **every 3 seconds**. That means:

- a connection that starts and ends between two reads is never seen at all;
- byte and packet counters are conntrack's, reported as deltas between two records;
- the start and end times are sample times, not the real first and last packet;
- one connection gives two records, one per direction, each with that direction's bytes and packets.

Short connections (a DNS query, a redirect) are what you lose. For per-packet accounting you need a
packet-level sensor; this is a sampled record of what was connected.

### ntopng

```bash
docker run -d --restart unless-stopped --name ntopng -p 3000:3000 ntop/ntopng:community \
  --http-port=3000 -i 127.0.0.1:2055
```

Then set the collector to `<host>:2055` with protocol `netflow9` (or `ipfix`). ntopng decodes both.

### GoFlow2

```bash
docker run -d --restart unless-stopped --name goflow2 -p 2055:2055/udp \
  -eGF_CORRELATE=false networkstatic/goflow:latest -listen=0.0.0.0:2055 -format=json -kafka=host:9092
```

GoFlow2 listens for both versions on one port and prints the records as JSON. With `-format=console` you can
watch them arrive before wiring up a sink:

```bash
docker run --rm -p 2055:2055/udp networkstatic/goflow:latest -listen=0.0.0.0:2055 -format=console
```

### nfdump / nfcapd

```bash
nfcapd -l -p 2055 -n /var/flowdata
```

`nfcapd` takes both v9 and IPFIX with templates (`-l`), then `nfdump -r /var/flowdata/...` reads them back.

