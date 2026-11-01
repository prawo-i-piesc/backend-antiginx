package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakePrometheus odpowiada na zapytania jak Prometheus z node-exporterem
// i kube-state-metrics: po fragmencie PromQL wybiera wynik.
func fakePrometheus(t *testing.T, vectors map[string]string, matrix string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if end := r.URL.Query().Get("end"); end != "" {
			if seconds, err := strconv.ParseInt(end, 10, 64); err != nil || seconds%30 != 0 {
				t.Errorf("koniec okna %s nie jest wyrównany do kroku", end)
			}
		}
		switch r.URL.Path {
		case "/-/ready":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/query":
			query := r.URL.Query().Get("query")
			// Najdłuższy pasujący fragment wygrywa: "node_load1" nie może
			// odpowiadać na "node_load15".
			result, best := "[]", 0
			for fragment, body := range vectors {
				if strings.Contains(query, fragment) && len(fragment) > best {
					result, best = body, len(fragment)
				}
			}
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":` + result + `}}`))
		case "/api/v1/query_range":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":` + matrix + `}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func vector(labels string, value string) string {
	return `{"metric":{` + labels + `},"value":[1730000000,"` + value + `"]}`
}

const node = `"instance":"10.0.0.5:9100"`

func TestCollectBuildsNodesWorkloadsAndHistory(t *testing.T) {
	server := fakePrometheus(t, map[string]string{
		`mode="idle"}[2m]`:                  `[` + vector(node, "37.5") + `]`,
		`count by (instance)`:               `[` + vector(node, "4") + `]`,
		`node_load1`:                        `[` + vector(node, "0.5") + `]`,
		`node_load5`:                        `[` + vector(node, "0.25") + `]`,
		`node_load15`:                       `[` + vector(node, "0.1") + `]`,
		`node_memory_MemTotal_bytes`:        `[` + vector(node, "8000") + `]`,
		`node_memory_MemAvailable_bytes`:    `[` + vector(node, "3000") + `]`,
		`node_filesystem_size_bytes`:        `[` + vector(node, "100000") + `]`,
		`node_filesystem_avail_bytes`:       `[` + vector(node, "40000") + `]`,
		`node_network_receive_bytes_total`:  `[` + vector(node, "1024") + `]`,
		`node_network_transmit_bytes_total`: `[` + vector(node, "512") + `]`,
		`node_boot_time_seconds`:            `[` + vector(node, "3600") + `]`,
		`node_uname_info`:                   `[` + vector(node+`,"nodename":"k3s-main"`, "1") + `]`,
		// backup-1 to zakończony job, a backend-0 pod sprzed rolloutu: nie ma
		// ich w fazach Pending/Running/Unknown, więc nie trafiają do tabeli.
		`kube_pod_status_phase`: `[` + vector(`"namespace":"antiginx","pod":"backend-1"`, "1") + `,` +
			vector(`"namespace":"data","pod":"postgres-0"`, "1") + `]`,
		`kube_pod_status_ready`: `[` + vector(`"namespace":"antiginx","pod":"backend-1"`, "1") + `,` +
			vector(`"namespace":"data","pod":"postgres-0"`, "0") + `,` +
			vector(`"namespace":"data","pod":"backup-1"`, "0") + `]`,
		`kube_pod_container_status_restarts_total`: `[` + vector(`"namespace":"data","pod":"postgres-0"`, "3") + `]`,
		`container_cpu_usage_seconds_total`: `[` + vector(`"namespace":"antiginx","pod":"backend-1"`, "0.05") + `,` +
			vector(`"namespace":"antiginx","pod":"backend-0"`, "0.2") + `]`,
		`container_memory_working_set_bytes`: `[` + vector(`"namespace":"antiginx","pod":"backend-1"`, "52428800") + `]`,
	}, `[{"metric":{},"values":[[1730000000,"10"],[1730000030,"NaN"],[1730000060,"12.5"]]}]`)
	defer server.Close()

	metrics, err := NewPrometheus(server.URL).Collect(context.Background(), time.Unix(1730000060, 0))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(metrics.Nodes) != 1 {
		t.Fatalf("węzły = %+v, want jeden", metrics.Nodes)
	}
	got := metrics.Nodes[0]
	want := Node{
		Name: "k3s-main", CPU: 37.5, Cores: 4, Load: [3]float64{0.5, 0.25, 0.1},
		MemoryUsed: 5000, MemoryTotal: 8000, DiskUsed: 60000, DiskTotal: 100000,
		NetworkRx: 1024, NetworkTx: 512, Uptime: 3600,
	}
	if got != want {
		t.Errorf("węzeł = %+v\nwant %+v", got, want)
	}

	if len(metrics.Workloads) != 2 {
		t.Fatalf("pody = %+v, want dwa żywe, bez zakończonego joba i poda sprzed rolloutu", metrics.Workloads)
	}
	backend, postgres := metrics.Workloads[0], metrics.Workloads[1]
	if backend.Pod != "backend-1" || !backend.Ready || backend.CPU != 0.05 || backend.Memory != 52428800 {
		t.Errorf("backend = %+v", backend)
	}
	if postgres.Pod != "postgres-0" || postgres.Ready || postgres.Restarts != 3 {
		t.Errorf("postgres = %+v, want niegotowy z 3 restartami", postgres)
	}

	// NaN wypada, bo JSON go nie zapisze.
	if len(metrics.History.CPU) != 2 || metrics.History.CPU[1].V != 12.5 {
		t.Errorf("historia CPU = %+v, want dwa punkty bez NaN", metrics.History.CPU)
	}
	if metrics.Window != 1800 || metrics.Step != 30 {
		t.Errorf("okno = %d s, krok = %d s", metrics.Window, metrics.Step)
	}

}

func TestCollectWithoutKubeMetricsLeavesWorkloadsEmpty(t *testing.T) {
	server := fakePrometheus(t, map[string]string{
		`node_memory_MemTotal_bytes`: `[` + vector(node, "8000") + `]`,
	}, `[]`)
	defer server.Close()

	metrics, err := NewPrometheus(server.URL).Collect(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(metrics.Workloads) != 0 {
		t.Errorf("pody = %+v, want brak", metrics.Workloads)
	}
	if metrics.History.CPU == nil || len(metrics.History.CPU) != 0 {
		t.Errorf("historia CPU = %#v, want pustą listę", metrics.History.CPU)
	}
	raw, _ := json.Marshal(metrics.History)
	if strings.Contains(string(raw), "null") {
		t.Errorf("historia w JSON = %s, want listy zamiast null", raw)
	}
}

func TestCollectFailsWhenAQueryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
	}))
	defer server.Close()

	if _, err := NewPrometheus(server.URL).Collect(context.Background(), time.Now()); err == nil {
		t.Fatal("Collect bez błędu, gdy Prometheus odrzuca zapytania")
	}
}

func TestReady(t *testing.T) {
	up := fakePrometheus(t, nil, `[]`)
	defer up.Close()
	if err := NewPrometheus(up.URL + "/").Ready(context.Background()); err != nil {
		t.Errorf("Ready: %v", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	if err := NewPrometheus(down.URL).Ready(context.Background()); err == nil {
		t.Error("Ready bez błędu przy 503")
	}
}
