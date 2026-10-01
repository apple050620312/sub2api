# Dynamic 5h pressure protection

Sub2API calculates a pool-wide pressure signal from the five-hour quota
snapshots already collected for active OpenAI/Codex, Anthropic, Kimi, Zhipu,
MiniMax, and OpenCode Go coding-plan accounts. It does not use a time-of-day
schedule.

## Signal

For every currently schedulable account with a valid snapshot, the service uses:

- the fraction consumed in the current five-hour window;
- the fraction still available; and
- that account's own remaining time until reset.

Disabled, expired, rate-limited, overloaded, temporarily unavailable, and
otherwise unschedulable accounts contribute no capacity. An account with an
exhausted 7d window remains excluded after its 5h reset and rejoins only after
the 7d limit has recovered and the account is schedulable again.

The pool burn rate is inferred from consumption so far. Projected demand until
the staggered resets is divided by remaining normalized capacity. Therefore an
account with little quota but an imminent reset contributes much less future
demand than one with the same quota and a distant reset.

An EWMA smooths short-lived changes. State transitions add hysteresis:

| State | Entry | Exit |
| --- | --- | --- |
| Normal | default, or pressure below `normal_threshold` | pressure reaches `peak_threshold` |
| Peak | pressure reaches `peak_threshold` | pressure falls below `normal_threshold` |

If there are no usable snapshots, enforcement fails open. Transient refresh
failures retain a bounded last-good snapshot; once it expires, enforcement
stops. Admission reads and reservations fail open on cache errors.

## Dynamic fair shares and borrowing

The service records successful metered usage per user in rolling five-minute
buckets in both Normal and Peak. It uses recent gateway demand to translate the
effective account pool into the same internal meter units. No administrator
configures a fixed money or token allowance.

Meter units are model-price-weighted cost. Prefer the precomputed upstream
model `UsageLog.AccountStatsCost` when available, without applying the account
statistics multiplier. Otherwise use `CostBreakdown.TotalCost`, including
the model's input/output/cache/image pricing, before user/group billing rate
multipliers (`ActualCost`). Invalid explicit upstream costs do not fall back
to customer pricing. Equal token counts on differently priced models
therefore consume different shares. Missing, zero, or invalid prices do not
fall back to raw tokens. This is a price-weighted estimate, not a claim that
model list prices exactly match native subscription quota consumption.
Native five-hour snapshots calibrate aggregate capacity; concurrent requests
prevent reliably attributing a snapshot delta to one request.

Price-based Redis keys use a new `price_v1` namespace. Old token buckets are
not converted or mixed with price-based usage; calibration warms up using new
traffic, with enforcement unavailable until usable price-based capacity exists.

At any instant, a user's fair share is the current effective five-hour pool
capacity divided by the users with demand in the last fifteen minutes, including
admitted pending requests. Usage itself remains rolling for five hours.
This value is recalculated as capacity and population change. There is
no Peak generation, fixed participant snapshot, or order-dependent allocation.

Normal adds no restriction, so active users may borrow capacity left idle by
others. When pressure enters Peak, the same rolling usage is compared with the
current dynamic fair share. Users below their share continue; users at or above
it are denied only on subsequent requests. Successful usage is never revoked.
A user joining during Peak immediately enters the denominator with zero usage
and receives the same dynamic fair-share treatment as everyone else.

Returning to Normal immediately stops enforcement and permits borrowing again.
Rolling usage is retained so a later Peak can still identify who consumed the
shared capacity during the preceding five hours. If there is not enough recent
traffic to calibrate capacity, enforcement fails open until calibration is
available.

## Configuration and status

Configuration is under `gateway.dynamic_5h_pressure`; all options and defaults
are shown in `deploy/config.example.yaml`.

- Admin: `GET /api/v1/admin/dashboard/5h-pressure` returns a freshly evaluated
  pool signal and its capacity inputs.
- User: `GET /api/v1/user/5h-pressure` returns the current state, 5h usage
  percentage, remaining percentage, recovery time, and limited flag. `100%`
  means the user's current dynamic fair share, so usage can exceed `100%`. It
  never exposes money, tokens, or internal capacity units.

用户限额页面同时公开账号池压力、有效及排除账号数量、活跃用户数量、
数据更新时间及最近重置时间。账号窗口区域支持搜索所有账号，展示名称、
平台、纳入或排除状态、5h/7d 用量百分比、重置时间及预计重新加入时间。
未取得或不支持的窗口显示“暂无窗口数据”，不会被当作零用量。
公开接口只使用明确列出的字段；不返回邮箱、凭证、原始 extra、用户列表或审计记录。
账号名称含邮箱格式时替换为账号编号。

倍率输入仅编辑草稿；点击“应用倍率”并确认旧值和新值后才提交。
按 Enter 或离开输入框不会提交。豁免和重置也需要确认。
操作原因不再收集或在接口、历史记录中展示；原有数据库记录保持兼容，
操作时间、操作者及修改前后值仍可审计。

The admin dashboard always displays the global signal and state. The user
dashboard displays a warning only while a temporary Peak rolling limit applies.

## Freshness and policy snapshots

Dynamic pressure denial maps to HTTP 429 with `Retry-After` derived from
`recover_at`; missing, invalid, or past recovery times use 60 seconds.
A pressure snapshot is usable for at most three refresh intervals, with a
minimum of 90 seconds. Missing timestamps or timestamps more than five seconds
in the future are invalid. Expired snapshots report `stale=true`, unavailable
calibration, and Normal state. Local configuration always controls whether the
guard is enabled. Historical rolling usage is retained during outages.

PostgreSQL policies are loaded in one batch and cached per instance for 30
seconds. A local policy update invalidates the snapshot immediately; another
instance sees it within 30 seconds when the database is healthy. During an
outage, an existing policy snapshot may be used for at most two minutes, after
which enforcement fails open. There are no per-user policy SQL queries in the
Peak request path.

## Durable meter reconciliation

Standard billing writes a `dynamic_5h_meter_events` event in the same PostgreSQL
transaction as request deduplication and billing effects. After commit, Redis
is updated immediately when available. Failed Redis writes or acknowledgments
remain pending and are retried by a bounded worker every ten seconds or when
new billing arrives. Redis receipts make retries idempotent, including the
failure window between Redis success and PostgreSQL acknowledgment.

Legacy/non-billing usage is enqueued separately with a request identity. It is
not transactionally coupled to a financial debit; an enqueue failure is counted
and cannot be recovered from this ledger. Events predating this migration are
not reconstructed from best-effort usage logs.

Admin-only `POST /api/v1/admin/dashboard/5h-pressure/reconcile` accepts
`{"replay":false,"after_id":0}` to retry pending events. Use `replay=true` to
rebuild from retained events after complete Redis loss. Each response processes
at most 250 records and returns `processed`, `repaired`, `expired`, `next_id`,
and `more`. Continue with `after_id=next_id` while `more=true`. A failed request
can be retried from the same cursor. Replays preserve original bucket times,
do not renew idle-user leases, and exclude user usage predating the latest
recorded usage reset while retaining pool consumption.

Events outside the five-hour window plus the conservative bucket boundary are
acknowledged as expired rather than charged into the present. Delivered ledger
rows older than seven days are removed in batches of 1,000 within the worker's
ten-second budget. Cleanup runs hourly after draining; unfinished cleanup retries
on the next worker cycle. Restore PostgreSQL
and Redis consistently; receipts identify events by database sequence ID.

The admin overview includes per-process `metrics`: refresh/policy failures,
stale and policy fail-open counts, reservation failures, meter read/write
failures, settlement failures, denials, reconciliation and cleanup failures, repaired
events, and expired events. Counters reset on restart and must be aggregated
across replicas by monitoring. Policy snapshots, metering reconciliation,
Redis ledger application, metrics, and traffic replay have separate modules.

## Replay evidence and allocation decision

Run from `backend`:

```sh
go run ./cmd/dynamic5h-replay -input internal/repository/testdata/dynamic5h-replay.json
go test -tags=unit ./internal/repository -run TestDynamic5hTrafficReplay -v
```

The tool uses the real admission service and Redis Lua scripts against isolated
in-memory Redis. Input supports arrivals, completion durations, changing state
and pool capacity, active users, exemptions, multipliers, and optional native
resource cost/budget labels. Pressure and calibrated capacity are supplied
inputs; the replay does not validate the upstream pressure estimator. Trace
timestamps and resource weights are synthetic in the supplied fixture, not
measurements of any provider's actual quota.

Equal price-weighted demand from five protected users against a capacity of 500 serves 100
units to each, with Jain index 1. Normal borrowing followed by Peak allows a
new user while rejecting subsequent excess borrowing from the earlier user.
With otherwise equal requests priced at 20 versus 2 units, replay admits five
expensive requests and fifty cheap requests against equal 100-unit shares.
The heterogeneous fixture gives both users 100 price units (Jain index 1), but
consumes 200% of the small resource budget and only 10% of the large budget.
Ten simultaneous 180-second requests of 20 units each are admitted against a
100-unit share because the current reservation is only 1% of that share; actual
completed usage can therefore exceed the estimate.

Decision: retain the existing global guard and administrator multipliers as
best-effort protection. For a guaranteed limit over independent heterogeneous
upstreams, route-constrained pools and measured resource weights are required;
the current global price estimate cannot provide that guarantee. Do not invent
provider/model weights from this synthetic fixture. Deployment-specific traces
and native quota measurements must determine the pool boundaries and weights.
Long-running request reservation estimation/renewal is a separate necessary
improvement before claiming a strict concurrent capacity bound.

## GHCR publication and rollback

The feature workflow waits for the latest push CI run on its exact commit and
branch to succeed. It first publishes an immutable full-SHA tag, then checks CI
again and confirms the branch still points at that commit before promoting the
digest to `latest` and `dynamic-5h-pressure`. Failed, cancelled, skipped,
unverified, and superseded commits cannot pass the publication gate.

The deployment updater retains the running image's digest (or a local tag made
from its image ID if no digest exists), backing up both its ID and reference.
Pull, recreation, configuration, or health failure restores a Compose file
pinned to that previous image with pulling disabled. A running container
without a health check is not considered healthy. Application rollback does
not automatically restore PostgreSQL: verify migration downgrade compatibility
or deliberately restore the retained database backup before using an older
binary that cannot read the updated schema.
