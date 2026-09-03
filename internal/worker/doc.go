// Package worker reserves the boundary for queue wake-ups, expiry processing and
// outbox delivery. No worker runs until the transactional use cases are present.
package worker
