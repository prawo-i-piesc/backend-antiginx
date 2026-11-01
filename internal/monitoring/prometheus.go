// Package monitoring zbiera stan systemu dla panelu admina: sondy zależności
// backendu i obciążenie serwera z Prometheusa, którego stawia
// kube-prometheus-stack w przestrzeni monitoring.
package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrPrometheus = errors.New("monitoring: prometheus query failed")

// Prometheus to klient HTTP API Prometheusa ograniczony do tego, czego
// potrzebuje panel: zapytania chwilowe, zakresowe i sprawdzenie gotowości.
type Prometheus struct {
	baseURL string
	client  *http.Client
}

func NewPrometheus(baseURL string) *Prometheus {
	return &Prometheus{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Sample to jedna seria w chwili zapytania.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Point to wartość w chwili T (sekundy od epoki).
type Point struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

type Series struct {
	Labels map[string]string
	Points []Point
}

type apiResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// Ready sprawdza, czy Prometheus przyjmuje zapytania.
func (p *Prometheus) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/-/ready", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: /-/ready answered %d", ErrPrometheus, resp.StatusCode)
	}
	return nil
}

// Query zwraca wektor chwilowy. Puste wyniki nie są błędem: metryki może po
// prostu jeszcze nie być, np. tuż po starcie eksportera.
func (p *Prometheus) Query(ctx context.Context, promql string) ([]Sample, error) {
	body, err := p.get(ctx, "/api/v1/query", url.Values{"query": {promql}})
	if err != nil {
		return nil, err
	}
	if body.Data.ResultType != "vector" {
		return nil, fmt.Errorf("%w: expected a vector, got %q", ErrPrometheus, body.Data.ResultType)
	}

	samples := make([]Sample, 0, len(body.Data.Result))
	for _, result := range body.Data.Result {
		_, value, ok := parsePair(result.Value)
		if !ok {
			continue
		}
		samples = append(samples, Sample{Labels: result.Metric, Value: value})
	}
	return samples, nil
}

// QueryRange zwraca serie z punktami co step od start do end.
func (p *Prometheus) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]Series, error) {
	body, err := p.get(ctx, "/api/v1/query_range", url.Values{
		"query": {promql},
		"start": {strconv.FormatInt(start.Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	})
	if err != nil {
		return nil, err
	}
	if body.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("%w: expected a matrix, got %q", ErrPrometheus, body.Data.ResultType)
	}

	series := make([]Series, 0, len(body.Data.Result))
	for _, result := range body.Data.Result {
		points := make([]Point, 0, len(result.Values))
		for _, pair := range result.Values {
			t, value, ok := parsePair(pair)
			if !ok {
				continue
			}
			points = append(points, Point{T: t, V: value})
		}
		series = append(series, Series{Labels: result.Metric, Points: points})
	}
	return series, nil
}

func (p *Prometheus) get(ctx context.Context, path string, query url.Values) (*apiResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPrometheus, err)
	}
	defer resp.Body.Close()

	var body apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: %s answered %d with an unreadable body", ErrPrometheus, path, resp.StatusCode)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("%w: %s: %s", ErrPrometheus, body.ErrorType, body.Error)
	}
	return &body, nil
}

// parsePair czyta parę [czas, "wartość"] z odpowiedzi Prometheusa. NaN i
// nieskończoności (np. dzielenie przez zero w PromQL) są pomijane, bo JSON
// ich nie zapisze.
func parsePair(pair []any) (int64, float64, bool) {
	if len(pair) != 2 {
		return 0, 0, false
	}
	t, ok := pair[0].(float64)
	if !ok {
		return 0, 0, false
	}
	raw, ok := pair[1].(string)
	if !ok {
		return 0, 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, 0, false
	}
	return int64(t), value, true
}
