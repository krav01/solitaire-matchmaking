package observability

import (
	"errors"
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

// DatabasePoolStats is a bounded snapshot of PostgreSQL pool health. Values are
// process-local and intentionally carry no database, host, or credential labels.
type DatabasePoolStats struct {
	AcquiredConnections  int32
	IdleConnections      int32
	TotalConnections     int32
	MaxConnections       int32
	AcquireCount         int64
	AcquireDuration      time.Duration
	CanceledAcquireCount int64
	EmptyAcquireCount    int64
	EmptyAcquireWait     time.Duration
}

// DatabasePoolStatsProvider returns a current PostgreSQL pool snapshot.
type DatabasePoolStatsProvider func() DatabasePoolStats

type databasePoolCollector struct {
	provider                 DatabasePoolStatsProvider
	acquiredConnections      *prometheus.Desc
	idleConnections          *prometheus.Desc
	totalConnections         *prometheus.Desc
	maxConnections           *prometheus.Desc
	acquireCount             *prometheus.Desc
	acquireDuration          *prometheus.Desc
	canceledAcquireCount     *prometheus.Desc
	emptyAcquireCount        *prometheus.Desc
	emptyAcquireWaitDuration *prometheus.Desc
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

// RegisterDatabasePool adds process-local PostgreSQL pool metrics to the
// registry. The provider is evaluated only when Prometheus scrapes the process.
func (metrics *Metrics) RegisterDatabasePool(provider DatabasePoolStatsProvider) error {
	if metrics == nil || provider == nil {
		return errors.New("database pool metrics provider is required")
	}
	if err := metrics.registry.Register(newDatabasePoolCollector(provider)); err != nil {
		return fmt.Errorf("register database pool metrics: %w", err)
	}

	return nil
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

func newDatabasePoolCollector(provider DatabasePoolStatsProvider) *databasePoolCollector {
	descriptor := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(
			prometheus.BuildFQName(metricNamespace, "database_pool", name), help, nil, nil,
		)
	}

	return &databasePoolCollector{
		provider: provider,
		acquiredConnections: descriptor(
			"acquired_connections", "PostgreSQL connections currently checked out from the pool.",
		),
		idleConnections: descriptor(
			"idle_connections", "PostgreSQL connections currently idle in the pool.",
		),
		totalConnections: descriptor(
			"total_connections", "PostgreSQL connections currently owned by the pool.",
		),
		maxConnections: descriptor(
			"max_connections", "Configured maximum PostgreSQL connections for this process.",
		),
		acquireCount: descriptor(
			"acquires_total", "Successful PostgreSQL pool acquisitions.",
		),
		acquireDuration: descriptor(
			"acquire_duration_seconds_total", "Cumulative duration of successful PostgreSQL pool acquisitions.",
		),
		canceledAcquireCount: descriptor(
			"canceled_acquires_total", "PostgreSQL pool acquisitions canceled by their context.",
		),
		emptyAcquireCount: descriptor(
			"empty_acquires_total", "Successful acquisitions that waited because the PostgreSQL pool was empty.",
		),
		emptyAcquireWaitDuration: descriptor(
			"empty_acquire_wait_seconds_total", "Cumulative wait for successful acquisitions while the PostgreSQL pool was empty.",
		),
	}
}

func (collector *databasePoolCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{
		collector.acquiredConnections, collector.idleConnections, collector.totalConnections,
		collector.maxConnections, collector.acquireCount, collector.acquireDuration,
		collector.canceledAcquireCount, collector.emptyAcquireCount,
		collector.emptyAcquireWaitDuration,
	} {
		descriptions <- description
	}
}

func (collector *databasePoolCollector) Collect(metrics chan<- prometheus.Metric) {
	stats := collector.provider()
	metrics <- prometheus.MustNewConstMetric(
		collector.acquiredConnections, prometheus.GaugeValue, float64(stats.AcquiredConnections),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.idleConnections, prometheus.GaugeValue, float64(stats.IdleConnections),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.totalConnections, prometheus.GaugeValue, float64(stats.TotalConnections),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.maxConnections, prometheus.GaugeValue, float64(stats.MaxConnections),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.acquireCount, prometheus.CounterValue, float64(stats.AcquireCount),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.acquireDuration, prometheus.CounterValue, stats.AcquireDuration.Seconds(),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.canceledAcquireCount, prometheus.CounterValue, float64(stats.CanceledAcquireCount),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.emptyAcquireCount, prometheus.CounterValue, float64(stats.EmptyAcquireCount),
	)
	metrics <- prometheus.MustNewConstMetric(
		collector.emptyAcquireWaitDuration, prometheus.CounterValue, stats.EmptyAcquireWait.Seconds(),
	)
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
