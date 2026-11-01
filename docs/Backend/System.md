# 🖥️ System status

The admin panel's System view: whether the backend's dependencies answer, how many scans are waiting, and how loaded the server is. The backend does not measure the machine itself. It asks the Prometheus that `kube-prometheus-stack` runs in the `monitoring` namespace, which already scrapes node-exporter (CPU, memory, disk, network, load), kube-state-metrics (pod readiness, restarts) and cAdvisor (pod CPU and memory). Grafana, from the same stack, stays the place for deeper digging; the panel links to it.

## 📡 Endpoint

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/admin/system` | Admin only: probes, scan queue and server load in one response. The panel polls it every few seconds. |

```json
{
  "checked_at": "2026-11-01T12:00:00Z",
  "checks": [
    { "name": "db", "status": "up", "latency_ms": 2 },
    { "name": "rabbitmq", "status": "up", "latency_ms": null },
    { "name": "prometheus", "status": "up", "latency_ms": 4 }
  ],
  "scans": { "pending": 3, "running": 1 },
  "metrics": {
    "nodes": [
      {
        "name": "k3s-main", "cpu": 37.5, "cores": 4, "load": [0.5, 0.25, 0.1],
        "memory_used": 5368709120, "memory_total": 8589934592,
        "disk_used": 21474836480, "disk_total": 64424509440,
        "network_rx": 10240, "network_tx": 4096, "uptime": 86400
      }
    ],
    "workloads": [
      { "namespace": "antiginx", "pod": "backend-deployment-7c9f…", "ready": true, "restarts": 0, "cpu": 0.004, "memory": 41943040 }
    ],
    "history": { "cpu": [{ "t": 1761998400, "v": 12.5 }], "memory": [], "network_rx": [], "network_tx": [] },
    "window": 1800,
    "step": 30
  },
  "grafana_url": "https://grafana.antiginx.pl"
}
```

- `checks` are the same probes as `/readyz` (`db`, `rabbitmq`) plus `prometheus`. `status` is `up` or `down`; Prometheus can also be `degraded` (it answers, but the queries fail) or `disabled` (`PROMETHEUS_URL` is not set).
- `latency_ms` is `null` for a disabled check and for `rabbitmq`, whose probe only reads the channel state in memory.
- `scans` counts free and premium scans together, since both wait in the same engine queue. It is `null` when the count fails or takes longer than the probe timeout, rather than a misleading zero.
- `metrics` is `null` when Prometheus is disabled, unreachable, or a query fails; the rest of the response still arrives.
- `cpu` is a percentage averaged over cores, `memory_*` and `disk_*` are bytes (the root filesystem), `network_*` bytes per second on physical interfaces, `uptime` seconds. A workload's `cpu` is in cores (`0.05` is 50m).
- `workloads` lists the live pods (phase `Pending`, `Running` or `Unknown`) of the `antiginx` and `data` namespaces, so finished backup jobs and pods replaced by a rollout do not show up as failures. CPU and memory come from the kubelet's cAdvisor endpoint. Without kube-state-metrics (for example a local Prometheus with only node-exporter) the list is empty.
- `history` covers the last 30 minutes at a 30-second step aligned to the step, for the whole cluster: CPU and memory in percent, network in bytes per second. Loopback, container bridges and the WireGuard tunnel are left out of network traffic.

## ⚙️ Configuration

| Variable | Example | Meaning |
| --- | --- | --- |
| `PROMETHEUS_URL` | `http://prometheus-operated.monitoring:9090` | Where the backend sends its PromQL queries. Unset turns the load part off. |
| `GRAFANA_URL` | `https://grafana.antiginx.pl` | Optional link shown in the panel. |

The `monitoring` namespace has no network policy, so pods in `antiginx` can reach Prometheus directly. Prometheus itself is never exposed to the browser: only administrators reach these figures, through the backend.
