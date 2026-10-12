package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/d0linger/treckrr/internal/config"
	"github.com/d0linger/treckrr/internal/metrics"
)

func TestOperationMetricsUseMuxPattern(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	count := func() int64 {
		var b strings.Builder
		metrics.Render(&b)
		for _, line := range strings.Split(b.String(), "\n") {
			if strings.HasPrefix(line, `treckrr_operation_duration_seconds_count{operation="neighbor_account"} `) {
				parts := strings.Fields(line)
				n, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
		}
		return 0
	}
	before := count()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /neighbors/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	// A noisy path suppresses access-log user resolution without affecting timing.
	r := httptest.NewRequest(http.MethodGet, "/neighbors/123?secret=private", nil)
	r = withUserCache(r)
	r.Context().Value(userCacheKey).(*userCache).done = true
	s.accessLog(mux).ServeHTTP(httptest.NewRecorder(), r)
	var out strings.Builder
	metrics.Render(&out)
	if count() != before+1 {
		t.Fatal("matched route was not observed")
	}
	if strings.Contains(out.String(), `operation="123"`) || strings.Contains(out.String(), "secret") {
		t.Fatal("request data leaked into metric")
	}
}
