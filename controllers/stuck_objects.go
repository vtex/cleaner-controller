/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// conditionalTTLStuckObjects gauges, per Ready-condition reason, how many
// ConditionalTTL objects are *currently* unable to progress (can't resolve
// their target, or have a broken CEL condition). This is meant to answer
// "is the cleaner healthy" more directly than
// controller_runtime_reconcile_total{result="error"}: that one is a
// reconcile-attempt rate, so a single permanently broken object gets
// diluted against the reconcile volume of every healthy sfj- object and
// barely moves the percentage, and it can't say which failure it is. This
// counts distinct objects, by reason, regardless of how many times each one
// has been retried.
//
// Only reasons that represent an actual stuck state are tracked - see the
// set/clear call sites in conditionalttl_controller.go. The routine waiting
// states (NotExpired, WaitingForConditions) and the terminal success state
// (Terminating) are deliberately excluded: counting those would make nearly
// every live object "stuck" and bury the signal this exists to surface.
var conditionalTTLStuckObjects = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "conditionalttl_stuck_objects",
	Help: "Number of ConditionalTTL objects currently unable to progress, by Ready-condition reason (e.g. TargetResolveError, ConditionCompileError). Excludes normal waiting states.",
}, []string{"reason"})

func init() {
	metrics.Registry.MustRegister(conditionalTTLStuckObjects)
}

// stuckObjectsTracker remembers the last-reported reason per object so the
// gauge reflects current state instead of accumulating. A stuck object gets
// reconciled repeatedly (retry backoff) while it stays broken, and again
// once it recovers; a plain Inc() on every reconcile would count the same
// object over and over instead of exactly once for as long as it's stuck.
type stuckObjectsTracker struct {
	mu       sync.Mutex
	byObject map[types.NamespacedName]string
}

var stuckObjects = &stuckObjectsTracker{byObject: make(map[types.NamespacedName]string)}

// set records that key is currently stuck with reason, adjusting the gauge
// if this is a new reason for it (or the first time it's stuck at all).
func (t *stuckObjectsTracker) set(key types.NamespacedName, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.byObject[key]; ok {
		if old == reason {
			return
		}
		conditionalTTLStuckObjects.WithLabelValues(old).Dec()
	}
	conditionalTTLStuckObjects.WithLabelValues(reason).Inc()
	t.byObject[key] = reason
}

// clear records that key is no longer stuck (deleted, healthy, or back to a
// normal waiting state).
func (t *stuckObjectsTracker) clear(key types.NamespacedName) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old, ok := t.byObject[key]
	if !ok {
		return
	}
	conditionalTTLStuckObjects.WithLabelValues(old).Dec()
	delete(t.byObject, key)
}
