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
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// conditionalTTLUpdateConflictsTotal counts optimistic-concurrency (HTTP 409)
// conflicts hit while writing a ConditionalTTL object or its status
// subresource. These resolve themselves on the next reconcile, once the
// client re-Gets the object's latest resourceVersion, and are not reconcile
// failures. controller-runtime's own controller_runtime_reconcile_errors_total
// metric can't tell a conflict apart from a real failure - both just bump
// that counter with the same "error" result label - so a dashboard/alert
// can't distinguish "self-healing noise" from "actually broken" without
// this. Watch reconcile_errors_total minus this counter instead of raw
// reconcile_errors_total.
var conditionalTTLUpdateConflictsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "conditionalttl_update_conflict_total",
	Help: "Number of HTTP 409 conflicts encountered updating a ConditionalTTL or its status, before a successful retry on the next reconcile.",
})

func init() {
	metrics.Registry.MustRegister(conditionalTTLUpdateConflictsTotal)
}

// handleUpdateErr turns the error from an r.Update/r.Status().Update call
// into a reconcile outcome. A conflict - the object was modified since we
// read it, e.g. by the controller's own prior status write racing a fresh
// watch-triggered reconcile - is expected and self-heals: it's logged at
// Warn instead of Error, counted separately from real failures, and
// answered with a requeue instead of a returned error. Returning the error
// here would make controller-runtime log it again at Error as "Reconciler
// error" and count it against controller_runtime_reconcile_errors_total
// alongside genuine failures.
func handleUpdateErr(log logr.Logger, err error) (ctrl.Result, error) {
	if err == nil {
		return ctrl.Result{}, nil
	}
	if !apierrors.IsConflict(err) {
		return ctrl.Result{}, err
	}
	conditionalTTLUpdateConflictsTotal.Inc()
	warn(log, err, "Conflict updating ConditionalTTL, retrying with latest version")
	return ctrl.Result{Requeue: true}, nil
}

// warn logs at the underlying zap logger's Warn level. logr, used
// throughout via log.FromContext, has no Warn method - only Info and Error
// - so downgrading a non-error condition to Info would bury it, and
// leaving it as Error is the exact miscategorization this exists to fix.
// The zapr sink configured in main.go implements zapr.Underlier, giving
// access to the real *zap.Logger; any other logr.LogSink (e.g. under test)
// falls back to Info so this never panics on the type assertion.
func warn(log logr.Logger, err error, msg string, keysAndValues ...interface{}) {
	kv := append(keysAndValues, "error", err.Error())
	if underlier, ok := log.GetSink().(zapr.Underlier); ok {
		underlier.GetUnderlying().Sugar().Warnw(msg, kv...)
		return
	}
	log.Info(msg, kv...)
}
