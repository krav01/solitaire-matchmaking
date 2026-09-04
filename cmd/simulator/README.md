# Simulator entry point

The command runs a deterministic matching-policy experiment and emits one JSON
report containing fill speed, timeout and fairness measures. Its defaults cover
weighted tournament partitions with room sizes five, six and seven. Every
arrival contains a simulated latent skill and a separate noisy pre-game rating
snapshot.

```bash
go run ./cmd/simulator \
  -seed 17 \
  -tickets 300 \
  -arrival-rate 5 \
  -start 2026-01-01T00:00:00Z \
  -initial-skill-gap 4 \
  -max-skill-gap 12 \
  -max-win-spread 0.35 \
  -expansion-interval 5s \
  -fill-timeout 30s \
  -age-priority-after 15s > report.json
```

Use `-output workload` to emit the reusable arrival stream without running the
matcher. The same arguments produce byte-identical output. Start time is
explicit and never taken from the wall clock. `internal/simulator.Generator`
produces a stable complete-room placement only after membership is known.
Outcome randomness is derived from the seed plus room, deck and player
identifiers, so changing call order does not change results.

Latent skill is simulator-only ground truth and must not enter candidate
selection. The report processes the workload through `pkg/matchmaking`, keeps
timed-out rooms visible in latency distributions and segments results by
tournament partition. Synthetic calibration validates behavior only and is not
a production accuracy claim.
