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
	if err := metrics.RegisterDatabasePool(func() observability.DatabasePoolStats {
		return observability.DatabasePoolStats{
			AcquiredConnections:  4,
			IdleConnections:      2,
			TotalConnections:     6,
			MaxConnections:       10,
			AcquireCount:         120,
			AcquireDuration:      3 * time.Second,
			CanceledAcquireCount: 1,
			EmptyAcquireCount:    5,
			EmptyAcquireWait:     250 * time.Millisecond,
		}
	}); err != nil {
		t.Fatalf("RegisterDatabasePool() error = %v", err)
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
		`solitaire_matchmaking_database_pool_acquired_connections 4`,
		`solitaire_matchmaking_database_pool_max_connections 10`,
		`solitaire_matchmaking_database_pool_acquires_total 120`,
		`solitaire_matchmaking_database_pool_acquire_duration_seconds_total 3`,
		`solitaire_matchmaking_database_pool_canceled_acquires_total 1`,
		`solitaire_matchmaking_database_pool_empty_acquire_wait_seconds_total 0.25`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("metrics output does not contain %q", expected)
		}
	}
}

func TestDatabasePoolMetricsRequireProvider(t *testing.T) {
	t.Parallel()

	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	if err := metrics.RegisterDatabasePool(nil); err == nil {
		t.Fatal("RegisterDatabasePool(nil) error = nil")
	}
}

func TestOperationalAssetsParse(t *testing.T) {
	t.Parallel()

	dashboard, err := os.ReadFile("../../deploy/observability/grafana-dashboard.json")
	if err != nil {
		t.Fatalf("read Grafana dashboard: %v", err)
	}
	var dashboardDocument struct {
		Panels []struct {
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(dashboard, &dashboardDocument); err != nil {
		t.Fatalf("parse Grafana dashboard: %v", err)
	}
	panelTitles := make(map[string]bool, len(dashboardDocument.Panels))
	for _, panel := range dashboardDocument.Panels {
		panelTitles[panel.Title] = true
	}
	for _, title := range []string{
		"HTTP business-route latency p99",
		"Ticket assignment latency p95",
		"PostgreSQL pool utilization",
		"PostgreSQL pool acquisition latency",
	} {
		if !panelTitles[title] {
			t.Errorf("Grafana dashboard is missing %q", title)
		}
	}

	alertContents, err := os.ReadFile("../../deploy/observability/prometheus-alerts.yaml")
	if err != nil {
		t.Fatalf("read Prometheus alerts: %v", err)
	}
	alertDocument := parsePrometheusRules(t, "alerts", alertContents)
	alertNames := make(map[string]bool)
	for _, group := range alertDocument.Groups {
		for _, rule := range group.Rules {
			alertNames[rule.Alert] = true
		}
	}
	for _, name := range []string{
		"SolitaireMatchmakingDown",
		"SolitaireMatchmakingFairnessLimitViolation",
		"SolitaireMatchmakingWorkerErrorsSustained",
	} {
		if !alertNames[name] {
			t.Errorf("Prometheus alerts are missing %q", name)
		}
	}

	sloContents, err := os.ReadFile("../../deploy/observability/prometheus-slo-pilot.yaml")
	if err != nil {
		t.Fatalf("read Prometheus pilot SLO rules: %v", err)
	}
	sloDocument := parsePrometheusRules(t, "pilot SLO", sloContents)
	recordNames := make(map[string]bool)
	pilotAlertNames := make(map[string]bool)
	for _, group := range sloDocument.Groups {
		for _, rule := range group.Rules {
			recordNames[rule.Record] = true
			pilotAlertNames[rule.Alert] = true
			if rule.Record != "" && (rule.Labels["environment"] != "pilot" || rule.Labels["objective"] == "") {
				t.Errorf("Prometheus pilot SLO rule %q has incomplete labels", rule.Record)
			}
			if rule.Alert != "" && rule.Labels["environment"] != "pilot" {
				t.Errorf("Prometheus pilot alert %q has no pilot environment label", rule.Alert)
			}
		}
	}
	for _, name := range []string{
		"solitaire_matchmaking:slo_http_availability:ratio_28d",
		"solitaire_matchmaking:slo_ticket_assignment_latency:ratio_28d",
		"solitaire_matchmaking:slo_worker_reliability:ratio_28d",
		"solitaire_matchmaking:slo_database_acquisition:ratio_28d",
	} {
		if !recordNames[name] {
			t.Errorf("Prometheus pilot SLO rules are missing %q", name)
		}
	}
	for _, name := range []string{
		"SolitaireMatchmakingHTTPLatencyHigh",
		"SolitaireMatchmakingTicketAssignmentSlow",
		"SolitaireMatchmakingWorkerFailureBudgetBurn",
		"SolitaireMatchmakingDatabasePoolSaturated",
		"SolitaireMatchmakingDatabaseAcquireBudgetBurn",
	} {
		if !pilotAlertNames[name] {
			t.Errorf("Prometheus pilot alerts are missing %q", name)
		}
	}
}

type prometheusRuleDocument struct {
	Groups []struct {
		Rules []struct {
			Alert  string            `yaml:"alert"`
			Record string            `yaml:"record"`
			Expr   string            `yaml:"expr"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func parsePrometheusRules(t *testing.T, name string, contents []byte) prometheusRuleDocument {
	t.Helper()

	var document prometheusRuleDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse Prometheus %s rules: %v", name, err)
	}
	if len(document.Groups) == 0 || len(document.Groups[0].Rules) == 0 {
		t.Fatalf("Prometheus %s rule file has no rules", name)
	}
	for _, group := range document.Groups {
		for _, rule := range group.Rules {
			if rule.Expr == "" || (rule.Alert == "" && rule.Record == "") {
				t.Fatalf("Prometheus %s rule file contains an incomplete rule", name)
			}
		}
	}

	return document
}
