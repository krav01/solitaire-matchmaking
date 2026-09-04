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
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(workload); err != nil {
		return fmt.Errorf("encode workload: %w", err)
	}
	return nil
}
