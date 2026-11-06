# Othello Edax Position Analysis

Othello RoomHost uses Edax 4.7 as a separate GPL-3.0 process to estimate the
current public position after every confirmed move. The Go service does not
link Edax code through CGO.

## Runtime Contract

- Binary: `/opt/edax/bin/edax`
- Evaluation weights: `/opt/edax/data/eval.dat`
- License: `/opt/edax/LICENSE`
- Corresponding source: `/opt/edax/source/edax-reversi-v4.7.tar.gz`
- Source tag: `v4.7`
- Source commit: `6c6991bee9a3d31d04f87934b04a7fd23c279f2b`
- `eval.dat` SHA-256:
  `f8b2299612d9fa4414157e70e932636e33111c2602d0c2fc382a7d90ef21b792`

Only the `roomhost-othello` image contains these assets.
The image build shallow-fetches the exact commit, verifies `HEAD`, and creates
the distributed source archive from that verified tree.

Each request starts an isolated NBoard process with one task, a one-second move
budget, and an 18-bit hash table. Edax 4.7 enforces one second as its minimum
time budget. The RoomHost runs at most one Edax process at a time. Its outer
request timeout is 2.5 seconds.

The adapter replays the authoritative `move` and `pass` log, runs `hint 1`, and
then closes stdin. It deliberately does not enqueue `quit`: Edax clears queued
commands when its input thread sees `quit`, which can otherwise discard the
pending `hint`. The adapter parses the final machine-readable line:

```text
search <principal variation> <score> 0 <depth>
```

The principal variation is discarded. The player UI receives only W/D/L
probabilities and search depth, never a recommended move.

## Probability Semantics

Edax returns an estimated final disc difference from the side-to-move
perspective. RoomHost first converts it to the black perspective, then applies
the Edax 4.7 evaluation-error model:

```text
x = -0.10026799 * empties + 0.31027733 * depth
sigma = 0.07585621 * x^2 + 1.16492647 * x + 5.4171698
```

The result is treated as the mean of a normal final-score distribution. Scores
between `-1` and `+1` form the draw band. The three resulting probabilities are
clamped and normalized to sum to one.

This value is a position estimate, not a historical player win rate and not a
perfect-play proof. A running position is always marked `exact=false`.
Terminal probabilities come from the platform's authoritative result and are
marked exact.

Edax awards remaining empty squares to the winner in its score convention,
while the platform compares actual discs only. This does not change the W/D/L
sign during analysis. Terminal percentages never use the Edax score.

## Delivery And Recovery

The move is committed and broadcast before analysis starts. The initial room
view carries `analysis.status=pending`. A background task later submits an
internal `analysis_result` command through the normal command log and snapshot
path.

Every position has a deterministic request ID. A newer position cancels the
old request, and RoomHost rejects any result whose active request ID no longer
matches. Pending requests are rescheduled after RoomHost recovery; ready
results are restored from state without another search.

Failures are represented as `analysis.status=unavailable` and never interrupt
gameplay. Diagnose:

- missing binary or evaluation file;
- process timeout or cancellation;
- malformed NBoard output;
- move-log replay mismatch;
- stale result drop;
- sustained queue timeout;
- RSS above the expected 65-85 MiB range.

Run the image contract check with:

```bash
./scripts/othello-edax-smoke.sh <roomhost-othello-image>
```
