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
	"errors"
	"testing"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestHandleUpdateErr(t *testing.T) {
	conflictErr := apierrors.NewConflict(
		schema.GroupResource{Group: "cleaner.vtex.io", Resource: "conditionalttls"},
		"sfj-c5bc713--umbroco",
		errors.New("the object has been modified; please apply your changes to the latest version and try again"),
	)

	before := testutil.ToFloat64(conditionalTTLUpdateConflictsTotal)

	res, err := handleUpdateErr(logr.Discard(), conflictErr)
	if err != nil {
		t.Fatalf("handleUpdateErr(conflict) returned err = %v, want nil (conflicts must not surface as reconcile errors)", err)
	}
	if !res.Requeue {
		t.Fatalf("handleUpdateErr(conflict) returned Requeue = false, want true so the controller retries with a fresh Get")
	}
	if got := testutil.ToFloat64(conditionalTTLUpdateConflictsTotal) - before; got != 1 {
		t.Fatalf("conditionalTTLUpdateConflictsTotal increased by %v, want 1", got)
	}

	res, err = handleUpdateErr(logr.Discard(), nil)
	if err != nil || res.Requeue {
		t.Fatalf("handleUpdateErr(nil) = (%v, %v), want (Result{}, nil)", res, err)
	}

	notFoundErr := apierrors.NewNotFound(
		schema.GroupResource{Group: "cleaner.vtex.io", Resource: "conditionalttls"},
		"sfj-c5bc713--umbroco",
	)
	res, err = handleUpdateErr(logr.Discard(), notFoundErr)
	if err != notFoundErr {
		t.Fatalf("handleUpdateErr(NotFound) swallowed a non-conflict error: got err = %v, want it returned unchanged so it still surfaces as a real reconcile failure", err)
	}
	if res.Requeue {
		t.Fatalf("handleUpdateErr(NotFound) set Requeue = true; only conflicts should self-requeue")
	}
}

// warn must not panic against a non-zapr logr.LogSink (e.g. the test
// no-op logger); it falls back to Info in that case.
func TestWarnFallsBackWithoutZaprSink(t *testing.T) {
	warn(logr.Discard(), errors.New("boom"), "conflict updating ConditionalTTL", "name", "sfj-c5bc713--umbroco")
}
