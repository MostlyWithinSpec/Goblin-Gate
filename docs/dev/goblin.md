# G.O.B.L.I.N. — Goblin Sentry's decision model

G.O.B.L.I.N. (Guarded Observation & Behavioral Logic for Incident Neutralization) learns how *you*
answer Goblin Sentry's incidents and handles the fuzzy middle: the incidents the rules would put in
front of you. It runs locally, never calls a cloud service and never changes itself: every model is
trained offline as a numbered version with an evaluation report, and is used only after you accept that
report. Code: `internal/sentry/goblin`.

## What it may do

- It is asked only when the rules say "ask the admin" (`request_approval`). Rules, including "Always do
  this" rules, always win; escalations about your own hosts always reach you.
- It answers with one tool call from a fixed list: `temporary_block_ip` (with a length from 15, 60, 240,
  1440 or 10080 minutes), `mark_false_positive`, `add_watch`, `notify_admin`, or "not sure". It never
  names an address: the engine fills the target from the incident, and goblind still checks every block
  against its protected targets.
- **Blocks always ask.** A block the model wants becomes the suggestion shown with the question ("G.O.B.L.I.N.
  suggests: block 198.51.100.7 for 1440 minutes (97% sure)"); you still press the button.
- With use `act`, a non-disruptive answer (ignore, watch, notify) above your threshold for that action runs
  on its own; below it, it is only the suggestion. With use `suggest` (the default) it never acts. In shadow
  mode it only records what it would have done.
- Every decision's "why" record carries the model's advice: version, action, confidence, and the three most
  similar past answers it rests on.

## Training and versions

Your answers (Block, Ignore, Approve, Always) are kept as examples in Sentry's state directory. Training
uses the newest 5000; Approve and Always count as "what Sentry recommended". The newest quarter is held
back: the model is trained on the older answers and asked about the newer ones, as it would have been at
the time. The report shows accuracy per action, the confusion table, how often it said block where you
ignored, how often it said ignore where you blocked (an attack it would have hidden), how often it would
have acted alone at each confidence threshold and how often it was wrong there, and how it did on
signatures it had never seen. The model you accept is then trained on all answers.

Accepting a version puts it in use; rollback returns to the version accepted before it (or to none). The
ten newest versions that were never accepted are kept. Sentry keeps working with rules and the approval
queue when no model is accepted or use is `off`.

API (Sentry's admin API, through goblind's plugin proxy at `/api/v1/plugins/sentry/api/`):

| Method and path | |
|---|---|
| `GET /models` | `{current, previous, policy, versions (newest first), job?}` |
| `GET /models/examples` | `{examples}`: your answers, oldest first |
| `DELETE /models/examples/{alert_id}` | forget a wrong answer |
| `POST /models/train` | `{kind?: knn \| llm, endpoint?, name?}` → `202 {job}` (runs in the background) |
| `GET /models/{v}` | a version with its report |
| `POST /models/{v}/accept` | put it in use |
| `POST /models/rollback` | back to the previous version |
| `PUT /models/policy` | `{use: off \| suggest \| act, thresholds: {tool: 0.5-1}}` |

Offline, the plugin program has the same tools: `run train -examples examples.jsonl -dir STATE`,
`run eval -examples F [-llm URL -name N]`, `run fixture` (the simulated dataset below) and
`run llm-data` (fine-tuning pairs, see below).

## The two candidates

**knn** (built in): a nearest-neighbour classifier over structured features — signature id and words,
category, severity, direction, local host, port, the remote's /24, how often the alert and the address were
seen, watch list, your earlier answers to the signature. The most similar past answers vote, weighted by
similarity and by age; the number of neighbours and the age half-life are picked by validating on the
newest quarter of the training answers. Confidence is the winning share of the vote times the similarity of
the closest answer, so an incident unlike anything you answered gets a low confidence and asks.

**llm**: FunctionGemma 270M run by llama.cpp's `llama-server`, fine-tuned on your answers, its prompt
showing the incident and your five most similar past answers, its output held by a GBNF grammar to exactly
the allowed calls (`goblin.Grammar()`; `goblin.ParseCall` accepts only those strings). Confidence is the
model's probability of the chosen tool's first token. Fine-tune on a desktop with a GPU:

```
run llm-data -examples examples.jsonl -train train.jsonl -test test.jsonl
python plugins/sentry/train/finetune.py --base functiongemma-270m-it --train train.jsonl \
    --out goblin-ft --gguf goblin-ft.gguf --llama-cpp /path/to/llama.cpp
llama-server -m goblin-ft.gguf --port 8090
run eval -examples examples.jsonl -llm http://127.0.0.1:8090 -name goblin-ft
```

## Comparison

Dataset: `goblin.Fixture(seed, 1500)`, a simulated admin who blocks exploits and block-listed sources,
ignores their own Plex server and stream noise, ignores SQL injection alerts from their pentest service,
watches first-time scanners and blocks repeat ones, and reports odd DNS from the laptop — with 4% random
answers, a change of mind about STUN halfway, and log4j attempts that appear only in the last 20% (never seen
in training). Trained on the oldest 1125 answers, evaluated on the newest 375. FunctionGemma fine-tuned for
3 epochs (full fine-tune, lr 5e-5, RTX 3090, about 4 minutes per run); both served on CPU.

| seed | model | accuracy | ignored what admin blocked | at 0.9: acted alone / wrong / hidden attacks | never-seen signatures right / acted alone | invalid calls | time per decision |
|---|---|---|---|---|---|---|---|
| 1 | knn | 94.9% | 6 | 110 / 4 / 2 | 14 of 19 / 0 | 0 | 0.7 ms |
| 1 | llm | 94.9% | 7 | 135 / 9 / 7 | 13 of 19 / 5 | 0 | 658 ms |
| 2 | knn | 93.1% | 3 | 84 / 0 / 0 | 20 of 24 / 0 | 0 | 0.7 ms |
| 2 | llm | 91.7% | 8 | 129 / 19 / 8 | 15 of 24 / 8 | 0 | 689 ms |
| 3 | knn | 97.3% | 0 | 102 / 2 / 0 | 14 of 14 / 0 | 0 | 0.7 ms |
| 3 | llm | 94.9% | 4 | 143 / 10 / 3 | 10 of 14 / 3 | 0 | 659 ms |

Untuned FunctionGemma 270M scored 2.4% (it almost always said "not sure"). Across 575 live calls including
200 deliberately odd incidents (prompt-injection text in the signature, nonsense severities and directions),
the grammar never let an invalid call through.

**Decision: knn.** It is as accurate or better on every seed, and — what matters for a model allowed to
ignore alerts — far better calibrated: when it is 90% sure it is almost always right, it never acted alone
on a signature it had never seen, and it hid 0–2 attacks where the fine-tuned model hid 3–8. It needs no
GPU, no extra service and no download, answers in under a millisecond inside the plugin's resource caps, and
explains itself with your own past answers. The language model stays available as kind `llm` (accepted the
same way, never allowed more than knn) for setups that want to try a larger or better-tuned model; it would
need a `llama-server` the admin runs.

## Resource limits

The plugin unit caps Sentry, and with it knn inference, at one CPU core and 512 MB
(`goblin-plugin@.service`: `CPUQuota=100%`, `MemoryMax=512M`). A knn model holds at most 5000 answers.
