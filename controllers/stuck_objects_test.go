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
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
)

func TestStuckObjectsTracker(t *testing.T) {
	tr := &stuckObjectsTracker{byObject: make(map[types.NamespacedName]string)}
	a := types.NamespacedName{Namespace: "faststore-proxy", Name: "sfj-a"}
	b := types.NamespacedName{Namespace: "faststore-proxy", Name: "sfj-b"}

	gaugeValue := func(reason string) float64 {
		return testutil.ToFloat64(conditionalTTLStuckObjects.WithLabelValues(reason))
	}

	tr.set(a, "TargetResolveError")
	if got := gaugeValue("TargetResolveError"); got != 1 {
		t.Fatalf("after set(a, TargetResolveError): gauge = %v, want 1", got)
	}

	// re-reconciling the same object with the same reason must not double count
	tr.set(a, "TargetResolveError")
	if got := gaugeValue("TargetResolveError"); got != 1 {
		t.Fatalf("after re-set(a, same reason): gauge = %v, want 1 (no double count)", got)
	}

	// a second object stuck for a different reason adds to that reason's gauge
	tr.set(b, "ConditionCompileError")
	if got := gaugeValue("ConditionCompileError"); got != 1 {
		t.Fatalf("after set(b, ConditionCompileError): gauge = %v, want 1", got)
	}
	if got := gaugeValue("TargetResolveError"); got != 1 {
		t.Fatalf("set(b, ...) affected TargetResolveError: gauge = %v, want unchanged 1", got)
	}

	// moving to a different reason decrements the old one and increments the new one
	tr.set(a, "ConditionEvaluationError")
	if got := gaugeValue("TargetResolveError"); got != 0 {
		t.Fatalf("after a moved reasons: TargetResolveError gauge = %v, want 0", got)
	}
	if got := gaugeValue("ConditionEvaluationError"); got != 1 {
		t.Fatalf("after a moved reasons: ConditionEvaluationError gauge = %v, want 1", got)
	}

	// recovering (clear) decrements and forgets the object
	tr.clear(a)
	if got := gaugeValue("ConditionEvaluationError"); got != 0 {
		t.Fatalf("after clear(a): ConditionEvaluationError gauge = %v, want 0", got)
	}
	if _, ok := tr.byObject[a]; ok {
		t.Fatalf("clear(a) left a tracked in byObject")
	}

	// clearing an object that was never stuck is a no-op, not a spurious decrement
	tr.clear(a)
	if got := gaugeValue("ConditionEvaluationError"); got != 0 {
		t.Fatalf("after redundant clear(a): ConditionEvaluationError gauge = %v, want 0 (must not go negative)", got)
	}

	tr.clear(b)
	if got := gaugeValue("ConditionCompileError"); got != 0 {
		t.Fatalf("after clear(b): ConditionCompileError gauge = %v, want 0", got)
	}
}
