# ☸️ Quick Start — Kubernetes

This repository ships no manifests. The snippets below are what the backend expects from a Deployment; keep the full manifests with the rest of your infrastructure.

## 🩺 Probes

The backend exposes two probes on port `4000`, outside `/api`, without CORS and without request logging:

| Path | Kind | Behaviour |
| --- | --- | --- |
| `/healthz` | liveness | `200 {"status":"ok"}` while the process serves HTTP. Checks nothing else, so a database outage does not restart the pod. |
| `/readyz` | readiness | Pings PostgreSQL (2-second timeout) and checks the RabbitMQ channel. `200 {"db":"up","rabbitmq":"up"}`, or `503` naming the dependency that is `down`. |

```yaml
containers:
  - name: backend-antiginx
    image: ghcr.io/prawo-i-piesc/backend-antiginx:latest
    ports:
      - name: http
        containerPort: 4000
    startupProbe:
      httpGet: { path: /healthz, port: http }
      periodSeconds: 2
      failureThreshold: 30
    livenessProbe:
      httpGet: { path: /healthz, port: http }
      periodSeconds: 10
      timeoutSeconds: 2
      failureThreshold: 3
    readinessProbe:
      httpGet: { path: /readyz, port: http }
      periodSeconds: 10
      timeoutSeconds: 3
      failureThreshold: 2
terminationGracePeriodSeconds: 20
```

`terminationGracePeriodSeconds` must exceed the backend's 10-second graceful drain after `SIGTERM`. The process exits during startup if PostgreSQL, migrations or RabbitMQ fail, so the startup probe only has to cover migration time.

The backend does not reconnect to RabbitMQ after the connection drops. The result consumers stop with the connection and the process shuts down gracefully, so the pod restarts and reconnects once the broker is back.

## 🔑 Secrets and scaling

Load `JWT_SECRET` and `TOTP_ENCRYPTION_KEY` from a `Secret`. Results come from the daemon through RabbitMQ, so backend replicas share the results queue: each replica runs `RESULT_CONSUMERS` consumers and the broker spreads results across all of them.

## 🌐 Behind an ingress

Set `TRUSTED_PROXIES` to the ingress pod CIDR (for k3s with Traefik usually `10.42.0.0/16`), otherwise per-IP limits see every request as coming from the ingress. Apply an egress `NetworkPolicy` to the engine pods that blocks cluster and private ranges: the backend rejects internal `target_url` values, but cannot stop a public domain that resolves to a private address.
