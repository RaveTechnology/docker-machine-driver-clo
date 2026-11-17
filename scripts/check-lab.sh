#!/usr/bin/env bash
# Read-only summary; requires an explicit context so the current context is irrelevant.
set -euo pipefail
if [[ $# -ne 2 ]]; then
 printf 'Usage: bash scripts/check-lab.sh MANAGEMENT_CONTEXT CLUSTER_NAME\n' >&2
 exit 2
fi
management_context="$1"
cluster_name="$2"
kubectl --context="$management_context" --request-timeout=20s get nodedriver clo \
 -o 'jsonpath={.status.appliedChecksum}{"\n"}{.status.conditions}{"\n"}'
kubectl --context="$management_context" --request-timeout=20s -n fleet-default \
 get clusters.provisioning.cattle.io "$cluster_name" \
 -o 'jsonpath={.status.conditions}{"\n"}'
kubectl --context="$management_context" --request-timeout=20s -n fleet-default \
 get machines.cluster.x-k8s.io -l "cluster.x-k8s.io/cluster-name=$cluster_name" -o wide
