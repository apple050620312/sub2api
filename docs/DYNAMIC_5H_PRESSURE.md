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

If there are no usable snapshots, or the refresh/Redis operation fails, the
guard fails open so it cannot disrupt existing gateway traffic.

## Dynamic fair shares and borrowing

The service records successful metered usage per user in rolling five-minute
buckets in both Normal and Peak. It uses recent gateway demand to translate the
effective account pool into the same internal meter units. No administrator
configures a fixed money or token allowance.

At any instant, a user's fair share is the current effective five-hour pool
capacity divided by the users active or requesting service in the rolling five
hours. This value is recalculated as capacity and population change. There is
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
- User: `GET /api/v1/user/5h-pressure` returns only the current state, 5h usage
  percentage, remaining percentage, recovery time, and limited flag. `100%`
  means the user's current dynamic fair share, so usage can exceed `100%`. It
  never exposes money, tokens, or internal capacity units.

The admin dashboard always displays the global signal and state. The user
dashboard displays a warning only while a temporary Peak rolling limit applies.
