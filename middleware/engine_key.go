package middleware

import (
	"crypto/subtle"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/prawo-i-piesc/backend/internal/httpx"
)

const EngineKeyHeader = "X-Engine-Key"

// RequireEngineKey wpuszcza tylko żądania z jednym z kluczy silnika w
// nagłówku X-Engine-Key. Lista zamiast jednego klucza pozwala na rotację bez
// przerwy: przy rolling restarcie część replik silnika ma jeszcze stary
// sekret, a część już nowy, i obie grupy muszą przechodzić.
//
// Pusta lista wyłącza bramkę, żeby backend dało się wdrożyć przed zmianą po
// stronie silnika; config.Load ostrzega o tym przy starcie.
func RequireEngineKey(keys []string) gin.HandlerFunc {
	accepted := make([][]byte, len(keys))
	for i, key := range keys {
		accepted[i] = []byte(key)
	}

	return func(c *gin.Context) {
		if len(accepted) == 0 {
			c.Next()
			return
		}

		presented := []byte(c.GetHeader(EngineKeyHeader))
		match := 0
		for _, key := range accepted {
			match |= subtle.ConstantTimeCompare(presented, key)
		}

		if match != 1 {
			// Wartości nagłówka nie logujemy nigdy: pomyłka w konfiguracji
			// silnika to zwykle poprzedni albo cudzy prawdziwy klucz.
			log.Printf("results: odrzucone wywołanie z %s", c.RemoteIP())
			httpx.Fail(c, httpx.CodeForbidden)
			return
		}
		c.Next()
	}
}
