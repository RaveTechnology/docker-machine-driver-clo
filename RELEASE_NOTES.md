Use the published clo-cloud-go v0.1.1 client to pace API requests and handle
transient HTTP 429 responses during reads. Default clients share a request
budget and provider cooldown; native mutations are not replayed.

An integration regression exercises the driver's actual client factory against
a throttled server read. Existing ownership, create and delete algorithms are
unchanged. This does not provide reconciliation for resources already orphaned
by a failed Rancher operation.

Linux/amd64 binary, deterministic archive, SHA256SUMS and NodeDriver manifest.
Rancher 2.15.3/RKE2; Apache-2.0.
