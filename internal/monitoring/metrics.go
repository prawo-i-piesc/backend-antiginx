package monitoring

import (
	"context"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Okno i krok wykresów w panelu: pół godziny co 30 s to 61 punktów na serię,
// dość, żeby zobaczyć skok, i mało, żeby odpowiedź była lekka przy
// odświeżaniu co kilka sekund.
const (
	HistoryWindow = 30 * time.Minute
	HistoryStep   = 30 * time.Second
)

// rate liczy się z dwóch minut: przy domyślnym scrapie co 30 s to cztery
// próbki, więc pojedynczy zgubiony scrape nie robi dziury w wykresie.
const rateWindow = "[2m]"

// Interfejsy wirtualne, mosty k3s/flannel i tunel WireGuard liczyłyby ten sam
// ruch drugi raz.
const physicalDevices = `device!~"lo|veth.*|cni.*|flannel.*|docker.*|br-.*|virbr.*|vxlan.*|cali.*|tunl.*|kube.*|wg.*"`

// Główny system plików węzła; tmpfs i nakładki kontenerów nie są dyskiem.
const rootFilesystem = `mountpoint="/",fstype!~"tmpfs|ramfs|overlay|squashfs"`

// Pody aplikacji i jej danych, jak je układa infrastructure-antiginx.
const appNamespaces = `namespace=~"antiginx|data"`

// cAdvisor z kubeleta, przypięty do swojego endpointu: gdyby kiedyś włączyć
// też /metrics/resource, te same kontenery nie policzą się dwa razy.
const cadvisor = `job="kubelet",metrics_path="/metrics/cadvisor",container!="",container!="POD"`

// Node to jeden węzeł klastra według node-exportera.
type Node struct {
	Name string `json:"name"`
	// CPU w procentach, 0–100, uśrednione po rdzeniach.
	CPU   float64 `json:"cpu"`
	Cores int     `json:"cores"`
	// Load average z 1, 5 i 15 minut.
	Load        [3]float64 `json:"load"`
	MemoryUsed  float64    `json:"memory_used"`
	MemoryTotal float64    `json:"memory_total"`
	DiskUsed    float64    `json:"disk_used"`
	DiskTotal   float64    `json:"disk_total"`
	// Bajty na sekundę na interfejsach fizycznych.
	NetworkRx float64 `json:"network_rx"`
	NetworkTx float64 `json:"network_tx"`
	// Sekundy od startu systemu.
	Uptime float64 `json:"uptime"`
}

// Workload to pod aplikacji według kube-state-metrics i cAdvisora.
type Workload struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Ready     bool   `json:"ready"`
	Restarts  int    `json:"restarts"`
	// Rdzenie, np. 0.05 to 50m.
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}

// History to przebieg obciążenia całego klastra w oknie HistoryWindow.
type History struct {
	CPU       []Point `json:"cpu"`
	Memory    []Point `json:"memory"`
	NetworkRx []Point `json:"network_rx"`
	NetworkTx []Point `json:"network_tx"`
}

type Metrics struct {
	Nodes     []Node     `json:"nodes"`
	Workloads []Workload `json:"workloads"`
	History   History    `json:"history"`
	// Sekundy, żeby front opisał oś bez zgadywania.
	Window int `json:"window"`
	Step   int `json:"step"`
}

// Collect pyta Prometheusa o wszystko naraz. Brak którejś metryki (np. bez
// kube-state-metrics nie ma podów) zostawia puste pole, a błąd zapytania
// przerywa całość: lepiej pokazać, że Prometheus nie odpowiada, niż pół
// prawdy.
func (p *Prometheus) Collect(ctx context.Context, now time.Time) (*Metrics, error) {
	instant := map[string]string{
		"cpu":          `(1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}` + rateWindow + `))) * 100`,
		"cores":        `count by (instance) (node_cpu_seconds_total{mode="idle"})`,
		"load1":        `node_load1`,
		"load5":        `node_load5`,
		"load15":       `node_load15`,
		"memTotal":     `node_memory_MemTotal_bytes`,
		"memAvailable": `node_memory_MemAvailable_bytes`,
		"diskTotal":    `max by (instance) (node_filesystem_size_bytes{` + rootFilesystem + `})`,
		"diskFree":     `max by (instance) (node_filesystem_avail_bytes{` + rootFilesystem + `})`,
		"netRx":        `sum by (instance) (rate(node_network_receive_bytes_total{` + physicalDevices + `}` + rateWindow + `))`,
		"netTx":        `sum by (instance) (rate(node_network_transmit_bytes_total{` + physicalDevices + `}` + rateWindow + `))`,
		"uptime":       `node_time_seconds - node_boot_time_seconds`,
		"uname":        `node_uname_info`,

		// Pody, które żyją: zakończone joby (np. kopia zapasowa bazy) i pody
		// sprzed rolloutu nie mają w tabeli udawać awarii.
		"podPhase":    `max by (namespace, pod) (kube_pod_status_phase{phase=~"Pending|Running|Unknown",` + appNamespaces + `}) == 1`,
		"podReady":    `max by (namespace, pod) (kube_pod_status_ready{condition="true",` + appNamespaces + `})`,
		"podRestarts": `sum by (namespace, pod) (kube_pod_container_status_restarts_total{` + appNamespaces + `})`,
		"podCPU":      `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{` + appNamespaces + `,` + cadvisor + `}` + rateWindow + `))`,
		"podMemory":   `sum by (namespace, pod) (container_memory_working_set_bytes{` + appNamespaces + `,` + cadvisor + `})`,
	}
	ranged := map[string]string{
		"cpu":       `(1 - avg(rate(node_cpu_seconds_total{mode="idle"}` + rateWindow + `))) * 100`,
		"memory":    `(1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes)) * 100`,
		"networkRx": `sum(rate(node_network_receive_bytes_total{` + physicalDevices + `}` + rateWindow + `))`,
		"networkTx": `sum(rate(node_network_transmit_bytes_total{` + physicalDevices + `}` + rateWindow + `))`,
	}

	var (
		mu      sync.Mutex
		samples = make(map[string][]Sample, len(instant))
		series  = make(map[string][]Point, len(ranged))
	)
	// Pierwszy błąd przerywa resztę zapytań, zamiast czekać na każde z osobna.
	group, groupCtx := errgroup.WithContext(ctx)

	for key, promql := range instant {
		group.Go(func() error {
			result, err := p.Query(groupCtx, promql)
			if err != nil {
				return err
			}
			mu.Lock()
			samples[key] = result
			mu.Unlock()
			return nil
		})
	}
	// Koniec okna wyrównany do kroku: kolejne odświeżenia trafiają w te same
	// chwile, więc linia na wykresie nie drga co 10 sekund.
	end := now.Truncate(HistoryStep)
	start := end.Add(-HistoryWindow)
	for key, promql := range ranged {
		group.Go(func() error {
			result, err := p.QueryRange(groupCtx, promql, start, end, HistoryStep)
			if err != nil {
				return err
			}
			var points []Point
			if len(result) > 0 {
				points = result[0].Points
			}
			mu.Lock()
			series[key] = points
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	return &Metrics{
		Nodes:     nodesFrom(samples),
		Workloads: workloadsFrom(samples),
		History: History{
			CPU:       orEmpty(series["cpu"]),
			Memory:    orEmpty(series["memory"]),
			NetworkRx: orEmpty(series["networkRx"]),
			NetworkTx: orEmpty(series["networkTx"]),
		},
		Window: int(HistoryWindow.Seconds()),
		Step:   int(HistoryStep.Seconds()),
	}, nil
}

// nodesFrom składa węzły po etykiecie instance, którą node-exporter daje
// każdej swojej metryce; nazwę bierze z node_uname_info.
func nodesFrom(samples map[string][]Sample) []Node {
	// Wolną pamięć i wolne miejsce trzyma osobno, aż będzie znana całość.
	type partial struct {
		Node
		memAvailable, diskFree float64
	}
	byInstance := map[string]*partial{}
	node := func(instance string) *partial {
		if n, ok := byInstance[instance]; ok {
			return n
		}
		n := &partial{Node: Node{Name: instance}}
		byInstance[instance] = n
		return n
	}
	set := func(key string, apply func(*partial, float64)) {
		for _, sample := range samples[key] {
			apply(node(sample.Labels["instance"]), sample.Value)
		}
	}

	set("cpu", func(n *partial, v float64) { n.CPU = v })
	set("cores", func(n *partial, v float64) { n.Cores = int(v) })
	set("load1", func(n *partial, v float64) { n.Load[0] = v })
	set("load5", func(n *partial, v float64) { n.Load[1] = v })
	set("load15", func(n *partial, v float64) { n.Load[2] = v })
	set("memTotal", func(n *partial, v float64) { n.MemoryTotal = v })
	set("memAvailable", func(n *partial, v float64) { n.memAvailable = v })
	set("diskTotal", func(n *partial, v float64) { n.DiskTotal = v })
	set("diskFree", func(n *partial, v float64) { n.diskFree = v })
	set("netRx", func(n *partial, v float64) { n.NetworkRx = v })
	set("netTx", func(n *partial, v float64) { n.NetworkTx = v })
	set("uptime", func(n *partial, v float64) { n.Uptime = v })

	names := map[string]string{}
	for _, sample := range samples["uname"] {
		if name := sample.Labels["nodename"]; name != "" {
			names[sample.Labels["instance"]] = name
		}
	}

	nodes := make([]Node, 0, len(byInstance))
	for instance, n := range byInstance {
		if instance == "" {
			continue
		}
		n.MemoryUsed = max(n.MemoryTotal-n.memAvailable, 0)
		n.DiskUsed = max(n.DiskTotal-n.diskFree, 0)
		if name, ok := names[instance]; ok {
			n.Name = name
		}
		nodes = append(nodes, n.Node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

// workloadsFrom bierze listę podów z kube-state-metrics (te w fazie
// Pending, Running albo Unknown) i dokłada do nich resztę. Zużycie z cAdvisora
// dla poda spoza tej listy — np. jeszcze widocznego w rate() po rolloucie —
// jest pomijane.
func workloadsFrom(samples map[string][]Sample) []Workload {
	type key struct{ namespace, pod string }
	byPod := map[key]*Workload{}
	for _, sample := range samples["podPhase"] {
		k := key{sample.Labels["namespace"], sample.Labels["pod"]}
		if k.pod != "" {
			byPod[k] = &Workload{Namespace: k.namespace, Pod: k.pod}
		}
	}
	attach := func(name string, apply func(*Workload, float64)) {
		for _, sample := range samples[name] {
			if w, ok := byPod[key{sample.Labels["namespace"], sample.Labels["pod"]}]; ok {
				apply(w, sample.Value)
			}
		}
	}
	attach("podReady", func(w *Workload, v float64) { w.Ready = v == 1 })
	attach("podRestarts", func(w *Workload, v float64) { w.Restarts = int(v) })
	attach("podCPU", func(w *Workload, v float64) { w.CPU = v })
	attach("podMemory", func(w *Workload, v float64) { w.Memory = v })

	workloads := make([]Workload, 0, len(byPod))
	for _, w := range byPod {
		workloads = append(workloads, *w)
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Namespace != workloads[j].Namespace {
			return workloads[i].Namespace < workloads[j].Namespace
		}
		return workloads[i].Pod < workloads[j].Pod
	})
	return workloads
}

// JSON ma pokazać pustą listę, a nie null, gdy metryki jeszcze nie ma.
func orEmpty(points []Point) []Point {
	if points == nil {
		return []Point{}
	}
	return points
}
