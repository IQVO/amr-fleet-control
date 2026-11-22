# Mutation testing: residual mutants

Gate: `.gremlins.yaml` (`efficacy: 99`, `mutant-coverage: 99`, plain numbers; gremlins fails when the measured value
is <= the threshold, so every package must measure above 99.00%). The `mutation-fast` CI job runs `gremlins unleash`
once per domain package (`./internal/domain/*/`, under `ulimit -v 4000000`); `make mutation` runs
`./internal/domain/mission`, `make mutation-full` and the weekly `mutation` job the aggregate `./internal/domain`.

Measured 2026-11-21 (Task 22; gremlins v0.6.0, `workers: 1`, `timeout-coefficient: 30`, cold test cache):

| Package | Killed | Lived | Not covered | Timed out | Efficacy | Mutator coverage |
|---|---|---|---|---|---|---|
| fleetmap | 56 | 0 | 0 | 0 | 100.00% | 100.00% |
| robot | 48 | 0 | 0 | 0 | 100.00% | 100.00% |
| mission | 115 | 1 | 0 | 0 | 99.14% | 100.00% |
| capacity | 8 | 0 | 0 | 0 | 100.00% | 100.00% |
| dispatch | 39 | 0 | 0 | 0 | 100.00% | 100.00% |

Aggregate (`make mutation-full`): 265 killed, 1 lived, 0 not covered, 1 timed out, efficacy 99.62%, mutator coverage
100.00%.

Before Task 22 (feature branch at `ce33ac7`, gate dormant at 0/0): fleetmap 82.19% / 97.33%, robot 95.65% / 95.83%,
mission 88.03% / 96.69%, capacity 100% / 87.50%, dispatch 75.76% / 80.49%; aggregate 239 killed, 37 lived, 17 not
covered.

## Mutants that cannot be killed

| Package | File:line | Mutator | Why |
|---|---|---|---|
| mission | mission.go:190:26 (`fits`, leg case) | CONDITIONALS_BOUNDARY (`dr > pick` to `dr >= pick`) | Equivalent. The two differ only at `dr == pick`. `actions` records one index per action and a step carries one `Action`, so the pick and the drop never share an index; `pick >= 0` is checked first, and an absent drop is `-1`, below every `pick >= 0`. No route makes `dr == pick`, so both forms accept and refuse the same routes. The comparison stays: it is the "drop after the pick" rule. |

## Timed out (counted by neither efficacy nor coverage)

| Package | File:line | Mutator | Note |
|---|---|---|---|
| fleetmap | route.go:35:10 (`Route`) | CONDITIONALS_NEGATION (`cur == to` to `cur != to`) | The search stops at `from`, so `rebuild` walks a missing predecessor chain and appends without bound. macOS cannot apply `ulimit -v`: in the per-package run above an RSS watchdog killed the test binary at 2 GB (the stand-in for CI's cap), so it counts KILLED; in the local aggregate run, without the watchdog, gremlins timed it out. Both CI mutation jobs run under `ulimit -v 4000000`, where it dies "out of memory" and counts KILLED, so CI's counts can exceed the local ones by this mutant. |

## Rewritten so an equivalent mutant cannot exist

Behaviour-preserving rewrites (own `refactor(...)` commits, existing tests unchanged):

| Package | Where | Before | After | Why the old mutant was equivalent |
|---|---|---|---|---|
| fleetmap | map.go `New` adjacency sort | `sort.Slice(..., links[i].to < links[j].to)` | `slices.SortFunc(..., strings.Compare(a.to, b.to))` | No two edges join the same pair, so neighbour ids are unique: `<` and `<=` sort alike. |
| fleetmap | map.go `pairKey` | `if a > b { a, b = b, a }` | `[2]string{min(a, b), max(a, b)}` | `a == b` (a loop) is refused before; the negated swap normalised every pair the other way round, equally unique. |
| fleetmap | route.go `rebuild` | hand-written `reverse` (`i < j`) | `slices.Reverse` | `i <= j` swapped the middle element with itself. |
| fleetmap | route.go `closest` | map-order walk, `... \|\| (abs(d - best) <= eps && n < best)` | ids visited in order; a later id wins only when nearer by more than eps | Map keys are unique, so `n < best` and `n <= best` chose alike. Differential probe over 200000 inputs whose within-eps classes are transitive: identical answers. On a non-transitive chain the old walk depended on Go's map order; the new one is deterministic. |
| fleetmap | deadlock.go `Cycles` order | `sort.Slice(..., out[i][0] < out[j][0])` | `slices.SortFunc(..., strings.Compare(a[0], b[0]))` | A robot waits on one blocker, so it is in one cycle at most: first keys are unique. |
| dispatch | dispatch.go `better` | tagless `switch`, `case c.Battery != best.Battery: return c.Battery > best.Battery`, `c.Robot.Key() < best.Robot.Key()` | if-chain, battery compared both ways, `strings.Compare(...) == -1` | The `!=` guard hid the `>=` mutant; candidate keys are unique. |
| dispatch | dispatch.go `Window` | `if limit > len(route)-1 { limit = len(route) - 1 }` | `min(reached+ahead, len(route)-1)` | At equality the `>=` mutant assigned the same value. |
| mission | transitions.go `Seen`, `Progress` | `if x > cur { cur = x }` | `cur = max(cur, x)` | At equality the `>=` mutant assigned the same value. |

Go's cover tool does not instrument the `case` expressions of a tagless `switch`, so their mutants were NOT COVERED
although every arm is tested. These became if-chains (tests unchanged) and are now covered and killed:
`robot.derive`, `capacity.Compute` (leg counts), `mission.Complete`, `dispatch.better` (15 NOT COVERED mutants
before, 0 after; the other 2 NOT COVERED were the `Cycles` comparator, first called by the two-cycle carry test).

## Boundary tests with exact floats

`fleetmap` and `dispatch` treat distances within `1e-9` as equal. Their boundary mutants (`<` vs `<=` at exactly
`x - 1e-9`) are killed by tests that place nodes on axis-aligned edges, where `math.Hypot(x, 0)` is exact, and compute
the boundary with the tolerance held in a `float64` variable: Go evaluates constant expressions exactly, so a
constant `2 - 1e-9` can round differently from the code's run-time `bestLen - eps`.
