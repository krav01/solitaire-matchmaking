package simulator

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

// Distribution reports sample values using linear-interpolated percentiles.
type Distribution struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

// FairnessMetrics are computed only for rooms that reached full capacity.
type FairnessMetrics struct {
	SkillGap                Distribution `json:"skill_gap"`
	WinProbabilitySpread    Distribution `json:"win_probability_spread"`
	ExpectedPlaceDispersion Distribution `json:"expected_place_dispersion"`
}

// Metrics keeps speed, timeout and fairness visible in the same segment.
type Metrics struct {
	Tickets                        int             `json:"tickets"`
	TicketsInFilledRooms           int             `json:"tickets_in_filled_rooms"`
	TicketsInTimedOutRooms         int             `json:"tickets_in_timed_out_rooms"`
	RoomsOpened                    int             `json:"rooms_opened"`
	RoomsFilled                    int             `json:"rooms_filled"`
	RoomsTimedOut                  int             `json:"rooms_timed_out"`
	FillTimeoutRate                float64         `json:"fill_timeout_rate"`
	PlayerWaitMilliseconds         Distribution    `json:"player_wait_ms"`
	RoomResolutionMilliseconds     Distribution    `json:"room_resolution_ms"`
	FilledRoomDurationMilliseconds Distribution    `json:"filled_room_duration_ms"`
	Fairness                       FairnessMetrics `json:"fairness"`
}

// PartitionReport is one homogeneous traffic and calibration segment.
type PartitionReport struct {
	Partition   TournamentPartition       `json:"partition"`
	Metrics     Metrics                   `json:"metrics"`
	Calibration *rating.CalibrationReport `json:"synthetic_calibration,omitempty"`
}

// Report is the complete deterministic result for one policy experiment.
type Report struct {
	Seed               uint64            `json:"seed"`
	StartedAt          time.Time         `json:"started_at"`
	ResolvedAt         time.Time         `json:"resolved_at"`
	PolicyVersion      string            `json:"policy_version"`
	RatingModelVersion string            `json:"rating_model_version"`
	WorkloadConfig     Config            `json:"workload_config"`
	RunConfig          RunConfig         `json:"run_config"`
	Overall            Metrics           `json:"overall"`
	Partitions         []PartitionReport `json:"partitions"`
	Notes              []string          `json:"notes"`
}

type metricSamples struct {
	tickets            int
	filledTickets      int
	timedOutTickets    int
	rooms              int
	filled             int
	timedOut           int
	playerWaitMS       []float64
	resolutionMS       []float64
	filledDurationMS   []float64
	skillGap           []float64
	winSpread          []float64
	expectedDispersion []float64
	observations       []rating.CalibrationObservation
}

func buildReport(workload Workload, config RunConfig, rooms []*simulatedRoom) (Report, error) {
	overallSamples := metricSamples{}
	partitionSamples := make(map[string]*metricSamples, len(workload.Config.TournamentPartitions))
	for _, partition := range workload.Config.TournamentPartitions {
		partitionSamples[partition.ID] = &metricSamples{}
	}
	resolvedAt := workload.Config.StartedAt

	for _, room := range rooms {
		partition := partitionSamples[room.partitionID]
		resolution := room.view.Deadline
		if room.filledAt != nil {
			resolution = *room.filledAt
		}
		if resolution.After(resolvedAt) {
			resolvedAt = resolution
		}
		if err := addRoomSamples(&overallSamples, room, resolution); err != nil {
			return Report{}, err
		}
		if err := addRoomSamples(partition, room, resolution); err != nil {
			return Report{}, err
		}
	}

	partitionReports := make([]PartitionReport, 0, len(workload.Config.TournamentPartitions))
	for _, partition := range workload.Config.TournamentPartitions {
		samples := partitionSamples[partition.ID]
		report := PartitionReport{Partition: partition, Metrics: samples.metrics()}
		if len(samples.observations) > 0 {
			calibration, err := rating.EvaluateCalibration(samples.observations, config.CalibrationBinCount)
			if err != nil {
				return Report{}, fmt.Errorf("calibrate partition %q: %w", partition.ID, err)
			}
			report.Calibration = &calibration
		}
		partitionReports = append(partitionReports, report)
	}

	workloadConfig := workload.Config
	workloadConfig.TournamentPartitions = slices.Clone(workload.Config.TournamentPartitions)
	return Report{
		Seed:               workload.Config.Seed,
		StartedAt:          workload.Config.StartedAt,
		ResolvedAt:         resolvedAt,
		PolicyVersion:      config.Policy.Version,
		RatingModelVersion: config.Policy.RatingModelVersion,
		WorkloadConfig:     workloadConfig,
		RunConfig:          config,
		Overall:            overallSamples.metrics(),
		Partitions:         partitionReports,
		Notes: []string{
			"An unmatched arrival opens a room immediately; ticket assignment latency is zero in this scenario.",
			"Timed-out rooms remain in player-wait and room-resolution distributions.",
			"Fairness and synthetic calibration include completed rooms only.",
			"Synthetic calibration validates behavior and is not a production accuracy claim.",
		},
	}, nil
}

func addRoomSamples(samples *metricSamples, room *simulatedRoom, resolution time.Time) error {
	if samples == nil {
		return errors.New("room references an unknown tournament partition")
	}
	samples.rooms++
	samples.tickets += len(room.arrivals)
	samples.resolutionMS = append(samples.resolutionMS, milliseconds(resolution.Sub(room.view.CreatedAt)))
	for _, arrival := range room.arrivals {
		samples.playerWaitMS = append(samples.playerWaitMS, milliseconds(resolution.Sub(arrival.ArrivedAt)))
	}
	if room.filledAt == nil {
		samples.timedOut++
		samples.timedOutTickets += len(room.arrivals)
		return nil
	}
	samples.filled++
	samples.filledTickets += len(room.arrivals)
	samples.filledDurationMS = append(samples.filledDurationMS, milliseconds(room.filledAt.Sub(room.view.CreatedAt)))
	samples.skillGap = append(samples.skillGap, roomSkillGap(room))
	spread, dispersion, err := predictionFairness(room.prediction)
	if err != nil {
		return fmt.Errorf("completed room %q fairness: %w", room.view.RoomID, err)
	}
	samples.winSpread = append(samples.winSpread, spread)
	samples.expectedDispersion = append(samples.expectedDispersion, dispersion)
	samples.observations = append(samples.observations, rating.CalibrationObservation{
		Prediction: room.prediction,
		Result:     room.result,
	})
	return nil
}

func (s metricSamples) metrics() Metrics {
	timeoutRate := 0.0
	if s.rooms > 0 {
		timeoutRate = float64(s.timedOut) / float64(s.rooms)
	}
	return Metrics{
		Tickets:                        s.tickets,
		TicketsInFilledRooms:           s.filledTickets,
		TicketsInTimedOutRooms:         s.timedOutTickets,
		RoomsOpened:                    s.rooms,
		RoomsFilled:                    s.filled,
		RoomsTimedOut:                  s.timedOut,
		FillTimeoutRate:                timeoutRate,
		PlayerWaitMilliseconds:         summarize(s.playerWaitMS),
		RoomResolutionMilliseconds:     summarize(s.resolutionMS),
		FilledRoomDurationMilliseconds: summarize(s.filledDurationMS),
		Fairness: FairnessMetrics{
			SkillGap:                summarize(s.skillGap),
			WinProbabilitySpread:    summarize(s.winSpread),
			ExpectedPlaceDispersion: summarize(s.expectedDispersion),
		},
	}
}

func predictionFairness(prediction rating.RoomPrediction) (float64, float64, error) {
	if len(prediction.Participants) == 0 {
		return 0, 0, errors.New("prediction has no participants")
	}
	minimumWin := math.Inf(1)
	maximumWin := math.Inf(-1)
	expectedMean := 0.0
	for _, participant := range prediction.Participants {
		if !finite(participant.FirstPlaceProbability) || !finite(participant.ExpectedPlace) {
			return 0, 0, errors.New("prediction contains non-finite fairness values")
		}
		minimumWin = math.Min(minimumWin, participant.FirstPlaceProbability)
		maximumWin = math.Max(maximumWin, participant.FirstPlaceProbability)
		expectedMean += participant.ExpectedPlace
	}
	expectedMean /= float64(len(prediction.Participants))
	variance := 0.0
	for _, participant := range prediction.Participants {
		difference := participant.ExpectedPlace - expectedMean
		variance += difference * difference
	}
	return maximumWin - minimumWin, math.Sqrt(variance / float64(len(prediction.Participants))), nil
}

func roomSkillGap(room *simulatedRoom) float64 {
	minimum := room.view.Members[0].Rating.Mean
	maximum := minimum
	for _, member := range room.view.Members[1:] {
		minimum = math.Min(minimum, member.Rating.Mean)
		maximum = math.Max(maximum, member.Rating.Mean)
	}
	return maximum - minimum
}

func summarize(values []float64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	total := 0.0
	for _, value := range ordered {
		total += value
	}
	return Distribution{
		Count: len(ordered),
		Mean:  total / float64(len(ordered)),
		P50:   percentile(ordered, 0.50),
		P90:   percentile(ordered, 0.90),
		P95:   percentile(ordered, 0.95),
		P99:   percentile(ordered, 0.99),
		Max:   ordered[len(ordered)-1],
	}
}

func percentile(ordered []float64, quantile float64) float64 {
	position := float64(len(ordered)-1) * quantile
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	weight := position - float64(lower)
	return ordered[lower]*(1-weight) + ordered[upper]*weight
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
