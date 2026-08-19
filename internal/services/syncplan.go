package services

import (
	"github.com/pratiknkulkarni/databasemanager/internal/database"
)

// syncPlan is the computed reconciliation between the secrets Infisical
// holds for an app and the contract a provisioning run wants recorded. It is
// executed as per-key operations (the SDK has no batch upsert), each of which
// is idempotent — a partial failure is healed by re-running.
type syncPlan struct {
	creates   []database.SecretKV // contract keys absent remotely
	updates   []database.SecretKV // contract keys present with a different value
	deletes   []string            // contract keys present remotely but absent from desired
	unchanged int                 // contract keys already holding the desired value
}

// planSecretSync computes the per-key operations that make the remote secret
// set match desired. Pure function — the heart of the upsert path.
//
// Only CONTRACT keys are ever deleted: a stale DB_SCHEMA (left over from an
// app's postgres past) would make every reader's contract validation reject
// the app, so it must go — but operator-owned extras stored in the same
// folder are never touched. Creates and updates preserve the contract's
// deterministic serialization order.
func planSecretSync(existing map[string]string, desired []database.SecretKV) syncPlan {
	var plan syncPlan

	desiredKeys := make(map[string]bool, len(desired))
	for _, kv := range desired {
		desiredKeys[kv.Key] = true
		current, ok := existing[kv.Key]
		switch {
		case !ok:
			plan.creates = append(plan.creates, kv)
		case current != kv.Value:
			plan.updates = append(plan.updates, kv)
		default:
			plan.unchanged++
		}
	}

	for _, key := range database.ContractKeys() {
		if _, ok := existing[key]; ok && !desiredKeys[key] {
			plan.deletes = append(plan.deletes, key)
		}
	}

	return plan
}
