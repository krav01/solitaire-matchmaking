# Simulator entry point

The command emits a deterministic JSON workload for the stage-3 simulator. Its
defaults cover weighted tournament partitions with room sizes five, six and
seven. Every arrival contains a simulated latent skill and a separate noisy
pre-game rating snapshot.

```bash
go run ./cmd/simulator \
  -seed 17 \
  -tickets 300 \
  -arrival-rate 5 \
  -start 2026-01-01T00:00:00Z > workload.json
```

The same arguments produce byte-identical output. Start time is explicit and
never taken from the wall clock. `internal/simulator.Generator` also produces a
stable complete-room placement after membership is known. Outcome randomness is
derived from the seed plus room, deck and player identifiers, so changing call
order does not change results.

Latent skill is simulator-only ground truth and must not enter candidate
selection. The next stage consumes this workload through `pkg/matchmaking` and
reports fill latency, timeout rate and fairness together; this command does not
publish fabricated quality metrics.
