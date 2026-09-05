# Game-backend integration example

This runnable receiver demonstrates the required authentication, body validation,
idempotency-key check and single-application behavior for matchmaking outbox
events.

```bash
export OUTBOX_DELIVERY_TOKEN="$(openssl rand -hex 32)"
export GAME_BACKEND_ADDR=127.0.0.1:8090
go run ./examples/game-backend
```

Configure the matchmaking service with the same token and
`OUTBOX_DELIVERY_URL=http://127.0.0.1:8090/events`. Plain HTTP is accepted only
for loopback development; deployed endpoints must use HTTPS.

The example keeps event digests in memory and serializes each side effect with
its deduplication decision. Restarting loses this state, the map grows with the
number of events, and a crash between logging and recording the digest can
repeat the log. Use it only for bounded local demonstrations. Payload object
key order is part of the example digest; the publisher replays a stable body.
A production game backend must instead store the
`event_id`, a digest of the event and its business side effect in one durable
database transaction. It should return 2xx for an identical replay, 409 for the
same identity with different data and a non-2xx response whenever the transaction
does not commit.

To submit an authoritative result in the other direction, use the request example
from [`api/openapi.yaml`](../../api/openapi.yaml), replace its room, session and
player identities with values allocated by the game backend, then send it with:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  --data @result.json \
  http://127.0.0.1:8080/v1/results
```

Retry the exact logical result with the same `event_id`. The first accepted
request returns 201; an identical replay returns 200 and `replay: true`.
