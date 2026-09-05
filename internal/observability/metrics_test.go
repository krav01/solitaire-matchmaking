package observability_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/observability"
	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"gopkg.in/yaml.v3"
)

func TestMetricsExposeBoundedOperationalSeries(t *testing.T) {
	t.Parallel()

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	metrics.ObserveHTTPRequest("GET", "/v1/rooms/{room_id}", 200, 25*time.Millisecond)
	metrics.ObserveWorkerCycle(worker.WorkerCycleObservation{
		Worker: worker.WorkerOutbox, Claimed: 3, Succeeded: 2, Failed: 1, Errored: true,
	})
	spread := 0.25
	metrics.ObserveMatch(worker.MatchObservation{
		Outcome: matchmaking.AttemptOutcomeMatched,
		ModeID:  "solitaire", Capacity: 5, PolicyVersion: "policy-v1",
		RatingModelVersion: "rating-v1", AssignmentLatency: 2 * time.Second,
		RoomFilled: true, RoomFillDuration: 20 * time.Second, FillTimeout: 30 * time.Second,
		SkillGap: 12, MaximumSkillGap: 10,
		WinProbabilitySpread: &spread, MaximumProbabilitySpread: 0.2,
	})

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 {
		t.Fatalf("metrics status = %d", response.Code)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	output := string(body)
	for _, expected := range []string{
		`solitaire_matchmaking_http_requests_total{method="GET",route="/v1/rooms/{room_id}",status="200"} 1`,
		`solitaire_matchmaking_worker_items_total{outcome="failed",worker="outbox"} 1`,
		`solitaire_matchmaking_matchmaking_rooms_filled_total{capacity="5",mode="solitaire",policy_version="policy-v1",rating_model_version="rating-v1"} 1`,
		`solitaire_matchmaking_matchmaking_fairness_violations_total{limit="skill_gap"} 1`,
		`solitaire_matchmaking_matchmaking_fairness_violations_total{limit="win_probability_spread"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("metrics output does not contain %q", expected)
		}
	}
}

func TestOperationalAssetsParse(t *testing.T) {
	t.Parallel()

	dashboard, err := os.ReadFile("../../deploy/observability/grafana-dashboard.json")
	if err != nil {
		t.Fatalf("read Grafana dashboard: %v", err)
	}
	var dashboardDocument struct {
		Panels []json.RawMessage `json:"panels"`
	}
	if err := json.Unmarshal(dashboard, &dashboardDocument); err != nil {
		t.Fatalf("parse Grafana dashboard: %v", err)
	}
	if len(dashboardDocument.Panels) == 0 {
		t.Fatal("Grafana dashboard has no panels")
	}

	alerts, err := os.ReadFile("../../deploy/observability/prometheus-alerts.yaml")
	if err != nil {
		t.Fatalf("read Prometheus alerts: %v", err)
	}
	var alertDocument struct {
		Groups []struct {
			Rules []map[string]any `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(alerts, &alertDocument); err != nil {
		t.Fatalf("parse Prometheus alerts: %v", err)
	}
	if len(alertDocument.Groups) == 0 || len(alertDocument.Groups[0].Rules) == 0 {
		t.Fatal("Prometheus alert file has no rules")
	}
}
