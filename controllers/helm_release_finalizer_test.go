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
	"context"
	"errors"
	"io"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	cleanerv1alpha1 "github.com/vtex/cleaner-controller/api/v1alpha1"
)

// This file only ever touches Helm's in-memory storage driver
// (driver.NewMemory()) - no real cluster, no real Helm release, nothing
// network-facing. It's the same fixture pattern Helm's own unit tests use
// (see helm.sh/helm/v3/pkg/action/action_test.go's actionConfigFixture).

func TestIsReleaseNotFoundErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sentinel unwrapped", driver.ErrReleaseNotFound, true},
		{
			"flattened by Uninstall.Run's purge-error path",
			errors.New("uninstallation completed with 1 error(s): uninstall: Failed to purge the release: release: not found"),
			true,
		},
		{"unrelated error", errors.New("some other failure"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isReleaseNotFoundErr(c.err); got != c.want {
				t.Fatalf("isReleaseNotFoundErr(%q) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestHelmReleaseFinalizer_AlreadyGone reproduces the tupan/trx incident: a
// ConditionalTTL's release-finalizer targeting a Helm release that no
// longer exists in storage. Both action.Get and action.Uninstall need a
// working KubeClient just to check IsReachable() before touching storage,
// so PrintingKubeClient (Helm's own no-op test double, writing to
// io.Discard) stands in for one - it never talks to a real cluster.
func TestHelmReleaseFinalizer_AlreadyGone(t *testing.T) {
	cfg := &action.Configuration{
		Releases:   storage.Init(driver.NewMemory()),
		KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
		Log:        func(string, ...interface{}) {},
	}

	r := &ConditionalTTLReconciler{HelmConfig: cfg}
	cTTL := &cleanerv1alpha1.ConditionalTTL{
		Spec: cleanerv1alpha1.ConditionalTTLSpec{
			Helm: &cleanerv1alpha1.HelmConfig{
				Release: "sfj-2f40163--tupan",
				Delete:  true,
			},
		},
	}

	if err := r.helmReleaseFinalizer(context.Background(), cTTL); err != nil {
		t.Fatalf("helmReleaseFinalizer() = %v, want nil (a release that never existed must not block the finalizer)", err)
	}
}

func TestHelmReleaseFinalizer_NoopWhenHelmSpecNil(t *testing.T) {
	r := &ConditionalTTLReconciler{}
	cTTL := &cleanerv1alpha1.ConditionalTTL{}
	if err := r.helmReleaseFinalizer(context.Background(), cTTL); err != nil {
		t.Fatalf("helmReleaseFinalizer() with nil Spec.Helm = %v, want nil", err)
	}
}

func TestHelmReleaseFinalizer_NoopWhenDeleteFalse(t *testing.T) {
	r := &ConditionalTTLReconciler{}
	cTTL := &cleanerv1alpha1.ConditionalTTL{
		Spec: cleanerv1alpha1.ConditionalTTLSpec{
			Helm: &cleanerv1alpha1.HelmConfig{Release: "whatever", Delete: false},
		},
	}
	if err := r.helmReleaseFinalizer(context.Background(), cTTL); err != nil {
		t.Fatalf("helmReleaseFinalizer() with Delete=false = %v, want nil", err)
	}
}
