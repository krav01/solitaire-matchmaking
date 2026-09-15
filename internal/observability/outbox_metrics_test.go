package observability_test

import (
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/krav01/solitaire-matchmaking/internal/observability"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"gopkg.in/yaml.v3"
)

func TestOutboxDeadLetterMetricAndAlert(t *testing.T) {
	t.Parallel()

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	metrics.ObserveWorkerCycle(worker.WorkerCycleObservation{
		Worker: worker.WorkerOutbox, Claimed: 1, DeadLettered: 1,
	})

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	if !strings.Contains(string(body), `solitaire_matchmaking_worker_items_total{outcome="dead_lettered",worker="outbox"} 1`) {
		t.Fatalf("dead-letter metric is missing from output: %s", body)
	}

	contents, err := os.ReadFile("../../deploy/observability/prometheus-alerts.yaml")
	if err != nil {
		t.Fatalf("read Prometheus alerts: %v", err)
	}
	var document struct {
		Groups []struct {
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse Prometheus alerts: %v", err)
	}
	for _, group := range document.Groups {
		for _, rule := range group.Rules {
			if rule.Alert == "SolitaireMatchmakingOutboxDeadLettered" {
				if !strings.Contains(rule.Expr, `outcome="dead_lettered"`) {
					t.Fatalf("dead-letter alert expression = %q", rule.Expr)
				}
				return
			}
		}
	}
	t.Fatal("SolitaireMatchmakingOutboxDeadLettered alert is missing")
}
