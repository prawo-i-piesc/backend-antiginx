package monitoring

import "context"

// Probe sprawdza jedną zależność, od której zależy obsługa ruchu. Name trafia
// do /readyz i do widoku systemu w panelu admina, więc ma być krótkie
// i stałe: "db", "rabbitmq".
type Probe struct {
	Name  string
	Check func(ctx context.Context) error
	// Local: sonda czyta stan w pamięci, bez wyjścia do sieci, więc jej czas
	// odpowiedzi nic nie mówi i widok systemu go nie pokazuje.
	Local bool
}
