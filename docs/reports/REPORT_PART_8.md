# Report Part 8: Grafana Dashboard and Scoreboard

## 1. Discovery ([discovery.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part8/discovery.txt))
- Auth Type: Anonymous (Admin role; GF_AUTH_ANONYMOUS_ENABLED=true).
- Datasource: `VictoriaMetrics` (UID `P4169E866C3094E38`, type `prometheus`).
- Metrics Found: `http_requests_total`, `http_request_duration_seconds_bucket`, `kube_deployment_status_replicas_ready`, `kube_deployment_spec_replicas`, `kube_horizontalpodautoscaler_status_desired_replicas`, `kube_horizontalpodautoscaler_spec_min_replicas`. Missing: `kube_customresource_*`, `scaledobject_*`.
- Prescaler Metrics: NO (pure outbound daemon; no Prometheus exporter, 0 `^prescale.*` series in VM).

## 2. Panels Built & Omitted
- Built: Scoreboard (Breach Seconds, Total Non-2xx, Pod-Seconds), Throughput (RPS), p99 Latency (SLO 300ms line), Error Rate (Non-2xx Share), Ready Pods & Desired Replicas, ScaledObject Min Replicas.
- Omitted: Forecast vs Actual (predictive) — NO FORECAST METRIC (prescaler exports no metrics).

## 3. Import Result ([import.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part8/import.txt))
- API import: `status: success`, `uid: prescale-demo`, `slug: prescale-demo3a-baseline-vs-predictive`, `version: 2`.

## 4. Panel Check & Scoreboard Comparison ([panel_check.txt](file:///home/unhealthycoder/MyBackHurts/docs/reports/data/part8/panel_check.txt))
- All timeseries panels verified with 2 series and 21–24 non-NaN points over B2 window (1793050834–1793051188).
- Scoreboard vs Part 7 B2 Table (MEASURED):
  - Breach seconds: baseline `80 s` (Grafana) vs `80 s` (Part 7) | predictive `0 s` (Grafana) vs `0 s` (Part 7)
  - Total non-2xx: baseline `0` (Grafana) vs `0` (Part 7) | predictive `0` (Grafana) vs `0` (Part 7)
  - Pod-seconds: baseline `2170` (Grafana) vs `2170` (Part 7) | predictive `2575` (Grafana) vs `2575` (Part 7)

## 5. AGENTS.md Conflicts & Scope Check
- AGENTS.md specifies Kubebuilder controller/scaffold removed in Part 2; active repo uses standalone daemon & KEDA.
- Scope: `git diff --stat START_HEAD..HEAD` touches only `deploy/grafana/**`, `scripts/grafana_import.sh`, `docs/**`.
- Prescaler replicas: 0; port-forwards: stopped.

## 6. Terminal Output
```text
$ git --no-pager status --short
# clean
$ git --no-pager log --oneline 38e5c57d1c23b24846d64eae121ffb35e6cfbad3..HEAD
b0cac09 docs: part 8 report
2092d30 measure: part 8 panel check
f69017f grafana: import script
bec79b6 grafana: demo dashboard json
9f61056 measure: part 8 discovery
```
