# Gateway Alert Notifications Design

## Goal

Add operator notifications for subscription refresh failures and a healthy-node
count below 30 without exposing subscription URLs, node endpoints, credentials,
or the notification token.

## Behavior

- `PUSH_BASE_URL` is an optional secret environment variable containing the
  notification service URL prefix. When absent, notifications are disabled.
- `low_node_threshold` is a positive YAML integer and defaults to `30`.
- A subscription alert is emitted when a source changes from healthy/unknown to
  failed. It identifies the source by its one-based source number and hostname,
  and says that the last-known-good snapshot remains in use.
- A source that stays failed does not generate repeated alerts. A successful
  refresh rearms that source so a later failure alerts again.
- A node-count alert is emitted when `healthy < low_node_threshold`. It is sent
  once per low period, rearmed silently after recovery to the threshold or above.
- A failed notification delivery remains pending and is retried on the next
  refresh or probe evaluation.
- Notifications are evaluated after the initial refresh/probe, every subscription
  refresh, and every health-probe cycle.

## Components and Data Flow

1. `internal/config` validates the optional HTTP/HTTPS push prefix and threshold.
2. `internal/notify` appends percent-escaped title/content path segments, issues a
   bounded GET request, rejects redirects, limits response consumption, and accepts
   only 2xx responses.
3. `internal/alert` owns edge-trigger state and formats Chinese messages from safe
   source labels, source errors, and aggregate pool statistics.
4. `internal/app` evaluates alerts only after state has been refreshed/probed.

## Security and Failure Handling

- The real push prefix is never committed or included in status output.
- Source labels expose only source number and hostname; URL paths and query tokens
  are never included.
- Redirects are rejected so the secret path cannot be forwarded to another host.
- Notification errors are logged with a generic failure class and never stop proxy
  serving, refresh, or health probing.

## Testing

## Expiry and integration addendum

Expiry comes from the `expire` Unix timestamp in `Subscription-Userinfo`.
Missing, zero, invalid, or out-of-range values are unknown, never inferred.
Successful responses update metadata even if node parsing fails; network failures
retain previous metadata. Snapshot copies isolate the metadata map.
The monitor sends a renewal notification when expiry is within 7 days (inclusive)
or already passed, once per source/expiry during this process lifetime. A renewed
expiry rearms notification. Restarting can repeat active alerts.

An independent worker samples settled state every 5 seconds after the initial
refresh finishes. Delivery does not hold the refresh/probe lock. Failures retry on
the next worker cycle. The admin status exposes expiry timestamps by source number.
Notifications name a source number and hostname and never include raw source errors.
HTTP delivery is supported because the operator supplied an HTTP service; HTTPS
is preferable when available. JSON application error codes also count as failures.

## Verification coverage

- Configuration tests cover defaults, valid optional URLs, and invalid schemes.
- Notification client tests cover escaped paths, 2xx handling, non-2xx failure,
  and redirect rejection using local HTTP test servers.
- Alert monitor tests cover first failure, deduplication, recovery/re-alert,
  threshold semantics, retry-after-send-failure, and secret-free source labels.
- Application tests verify alert evaluation after refresh/probe integration.
