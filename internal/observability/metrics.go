package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/worker"
	"github.com/krav01/solitaire-matchmaking/pkg/matchmaking"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricNamespace = "solitaire_matchmaking"

type Metrics struct {
	registry                   *prometheus.Registry
	httpRequests               *prometheus.CounterVec
	httpDuration               *prometheus.HistogramVec
	matchAttempts              *prometheus.CounterVec
	ticketAssignmentDuration   *prometheus.HistogramVec
	roomsFilled                *prometheus.CounterVec
	roomFillDuration           *prometheus.HistogramVec
	roomFillTimeoutUtilization *prometheus.HistogramVec
	roomSkillGap               *prometheus.HistogramVec
	roomSkillGapRatio          *prometheus.HistogramVec
	roomProbabilitySpread      *prometheus.HistogramVec
	roomProbabilitySpreadRatio *prometheus.HistogramVec
	fairnessViolations         *prometheus.CounterVec
	workerCycles               *prometheus.CounterVec
	workerItems                *prometheus.CounterVec
}

func NewMetrics() (*Metrics, error) {
	labels := []string{"mode", "capacity", "policy_version", "rating_model_version"}
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "http", Name: "requests_total",
			Help: "HTTP requests by stable route, method and response status.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "http", Name: "request_duration_seconds",
			Help:    "HTTP request duration by stable route and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		matchAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "attempts_total",
			Help: "Persisted matchmaking decisions by outcome.",
		}, []string{"outcome"}),
		ticketAssignmentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "ticket_assignment_seconds",
			Help:    "Time from ticket acceptance to successful room assignment.",
			Buckets: durationBuckets(),
		}, labels),
		roomsFilled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "rooms_filled_total",
			Help: "Rooms successfully filled by mode, size and immutable policy versions.",
		}, labels),
		roomFillDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_fill_seconds",
			Help:    "Time from room creation to successful final assignment.",
			Buckets: durationBuckets(),
		}, labels),
		roomFillTimeoutUtilization: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_fill_timeout_ratio",
			Help:    "Room fill duration divided by the immutable policy fill timeout.",
			Buckets: ratioBuckets(),
		}, labels),
		roomSkillGap: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_skill_gap",
			Help:    "Final room rating mean gap measured before play.",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 10),
		}, labels),
		roomSkillGapRatio: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_skill_gap_limit_ratio",
			Help:    "Final room skill gap divided by its hard policy limit.",
			Buckets: ratioBuckets(),
		}, labels),
		roomProbabilitySpread: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_win_probability_spread",
			Help:    "Final room maximum minus minimum predicted first-place probability.",
			Buckets: prometheus.LinearBuckets(0.05, 0.05, 20),
		}, labels),
		roomProbabilitySpreadRatio: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "room_win_probability_spread_limit_ratio",
			Help:    "Final room probability spread divided by its hard policy limit.",
			Buckets: ratioBuckets(),
		}, labels),
		fairnessViolations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "matchmaking", Name: "fairness_violations_total",
			Help: "Successful final assignments observed outside a hard fairness limit.",
		}, []string{"limit"}),
		workerCycles: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "worker", Name: "cycles_total",
			Help: "Background worker cycles by worker and result.",
		}, []string{"worker", "result"}),
		workerItems: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: "worker", Name: "items_total",
			Help: "Background work items by worker and outcome.",
		}, []string{"worker", "outcome"}),
	}

	registeredCollectors := []prometheus.Collector{
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.httpRequests, metrics.httpDuration, metrics.matchAttempts,
		metrics.ticketAssignmentDuration, metrics.roomsFilled, metrics.roomFillDuration,
		metrics.roomFillTimeoutUtilization, metrics.roomSkillGap, metrics.roomSkillGapRatio,
		metrics.roomProbabilitySpread, metrics.roomProbabilitySpreadRatio,
		metrics.fairnessViolations, metrics.workerCycles, metrics.workerItems,
	}
	for _, collector := range registeredCollectors {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register metrics collector: %w", err)
		}
	}

	return metrics, nil
}

func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (metrics *Metrics) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	metrics.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	metrics.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (metrics *Metrics) ObserveWorkerCycle(observation worker.WorkerCycleObservation) {
	result := "success"
	if observation.Errored {
		result = "error"
	}
	metrics.workerCycles.WithLabelValues(observation.Worker, result).Inc()
	addCounter(metrics.workerItems.WithLabelValues(observation.Worker, "claimed"), observation.Claimed)
	addCounter(metrics.workerItems.WithLabelValues(observation.Worker, "succeeded"), observation.Succeeded)
	addCounter(metrics.workerItems.WithLabelValues(observation.Worker, "failed"), observation.Failed)
}

func (metrics *Metrics) ObserveMatch(observation worker.MatchObservation) {
	metrics.matchAttempts.WithLabelValues(string(observation.Outcome)).Inc()
	if observation.Outcome != matchmaking.AttemptOutcomeMatched {
		return
	}

	labels := []string{
		observation.ModeID, strconv.Itoa(observation.Capacity),
		observation.PolicyVersion, observation.RatingModelVersion,
	}
	metrics.ticketAssignmentDuration.WithLabelValues(labels...).Observe(observation.AssignmentLatency.Seconds())
	if !observation.RoomFilled {
		return
	}

	metrics.roomsFilled.WithLabelValues(labels...).Inc()
	metrics.roomFillDuration.WithLabelValues(labels...).Observe(observation.RoomFillDuration.Seconds())
	metrics.roomFillTimeoutUtilization.WithLabelValues(labels...).Observe(
		ratio(observation.RoomFillDuration.Seconds(), observation.FillTimeout.Seconds()),
	)
	metrics.roomSkillGap.WithLabelValues(labels...).Observe(observation.SkillGap)
	metrics.roomSkillGapRatio.WithLabelValues(labels...).Observe(
		ratio(observation.SkillGap, observation.MaximumSkillGap),
	)
	if observation.SkillGap > observation.MaximumSkillGap {
		metrics.fairnessViolations.WithLabelValues("skill_gap").Inc()
	}
	if observation.WinProbabilitySpread == nil {
		return
	}

	spread := *observation.WinProbabilitySpread
	metrics.roomProbabilitySpread.WithLabelValues(labels...).Observe(spread)
	metrics.roomProbabilitySpreadRatio.WithLabelValues(labels...).Observe(
		ratio(spread, observation.MaximumProbabilitySpread),
	)
	if spread > observation.MaximumProbabilitySpread {
		metrics.fairnessViolations.WithLabelValues("win_probability_spread").Inc()
	}
}

func addCounter(counter prometheus.Counter, value int) {
	if value > 0 {
		counter.Add(float64(value))
	}
}

func durationBuckets() []float64 {
	return []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}
}

func ratioBuckets() []float64 {
	return []float64{0.1, 0.25, 0.5, 0.7, 0.8, 0.9, 1}
}

func ratio(value, limit float64) float64 {
	if limit == 0 {
		if value > 0 {
			return 2
		}
		return 0
	}

	return value / limit
}
