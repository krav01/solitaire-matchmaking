package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/krav01/solitaire-matchmaking/internal/simulator"
	"github.com/krav01/solitaire-matchmaking/pkg/rating"
)

const defaultStartTime = "2026-01-01T00:00:00Z"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("simulator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	seed := flags.Uint64("seed", 1, "deterministic workload seed")
	tickets := flags.Int("tickets", 300, "number of ticket arrivals")
	arrivalRate := flags.Float64("arrival-rate", 5, "mean arrivals per second")
	startValue := flags.String("start", defaultStartTime, "RFC3339 simulation start time")
	outputMode := flags.String("output", "report", "output type: report or workload")
	initialSkillGap := flags.Float64("initial-skill-gap", 4, "initial matchmaking skill gap")
	maximumSkillGap := flags.Float64("max-skill-gap", 12, "hard maximum matchmaking skill gap")
	maximumWinSpread := flags.Float64("max-win-spread", 0.35, "hard maximum first-place probability spread")
	expansionInterval := flags.Duration("expansion-interval", 5*time.Second, "skill-window expansion interval")
	fillTimeout := flags.Duration("fill-timeout", 30*time.Second, "room fill timeout")
	agePriorityAfter := flags.Duration("age-priority-after", 15*time.Second, "waiting age before room priority")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("parse simulator flags: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("simulator does not accept positional arguments")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, *startValue)
	if err != nil {
		return fmt.Errorf("parse simulation start time: %w", err)
	}

	config := simulator.DefaultConfig(startedAt, *seed)
	config.TicketCount = *tickets
	config.ArrivalRatePerSecond = *arrivalRate
	generator, err := simulator.New(config)
	if err != nil {
		return fmt.Errorf("configure simulator: %w", err)
	}
	workload, err := generator.GenerateWorkload()
	if err != nil {
		return fmt.Errorf("generate workload: %w", err)
	}

	var result any
	switch *outputMode {
	case "workload":
		result = workload
	case "report":
		model, err := rating.NewBaseline(rating.DefaultBaselineConfig(config.RatingModelVersion))
		if err != nil {
			return fmt.Errorf("configure rating model: %w", err)
		}
		runConfig := simulator.DefaultRunConfig(config.RatingModelVersion)
		runConfig.Policy.InitialSkillGap = *initialSkillGap
		runConfig.Policy.MaxSkillGap = *maximumSkillGap
		runConfig.Policy.MaxWinProbabilitySpread = *maximumWinSpread
		runConfig.Policy.ExpansionInterval = *expansionInterval
		runConfig.Policy.FillTimeout = *fillTimeout
		runConfig.Policy.AgePriorityAfter = *agePriorityAfter
		runner, err := simulator.NewRunner(model, generator, runConfig)
		if err != nil {
			return fmt.Errorf("configure simulation run: %w", err)
		}
		report, err := runner.Run(workload)
		if err != nil {
			return fmt.Errorf("run simulation: %w", err)
		}
		result = report
	default:
		return fmt.Errorf("unsupported output type %q", *outputMode)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode simulation output: %w", err)
	}
	return nil
}
