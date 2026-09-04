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
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
)

func TestClientForNamespace_PrefersBearerTokenFileOverStaticToken(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token-from-file\n"), 0o600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}

	r := &ConditionalTTLReconciler{
		Config: &rest.Config{
			Host:            "https://api.example.com",
			BearerToken:     "stale-startup-token",
			BearerTokenFile: tokenFile,
		},
	}

	flags, err := r.clientForNamespace("faststore-proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *flags.BearerToken; got != "token-from-file" {
		t.Errorf("expected token read from BearerTokenFile (trimmed), got %q", got)
	}
	if got := *flags.APIServer; got != "https://api.example.com" {
		t.Errorf("expected APIServer to be propagated, got %q", got)
	}
	if got := *flags.Namespace; got != "faststore-proxy" {
		t.Errorf("expected Namespace to be propagated, got %q", got)
	}
}

func TestClientForNamespace_RereadsTokenFileOnEveryCall(t *testing.T) {
	// This is the core regression test: the original bug captured the
	// bearer token once at process startup and reused it for the pod's
	// entire lifetime. Once the kubelet rotated the projected
	// service-account token on disk, every subsequent Helm operation kept
	// using the stale value and failed with
	// "the server has asked for the client to provide credentials".
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("first-token"), 0o600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}
	r := &ConditionalTTLReconciler{
		Config: &rest.Config{BearerTokenFile: tokenFile},
	}

	first, err := r.clientForNamespace("ns")
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}
	if got := *first.BearerToken; got != "first-token" {
		t.Fatalf("expected %q, got %q", "first-token", got)
	}

	// simulate the kubelet rotating the projected token on disk
	if err := os.WriteFile(tokenFile, []byte("rotated-token"), 0o600); err != nil {
		t.Fatalf("failed to rewrite token file: %v", err)
	}

	second, err := r.clientForNamespace("ns")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if got := *second.BearerToken; got != "rotated-token" {
		t.Errorf("expected the rotated token to be picked up on the next call, got %q", got)
	}
}

func TestClientForNamespace_FallsBackToStaticTokenWhenFileUnset(t *testing.T) {
	r := &ConditionalTTLReconciler{
		Config: &rest.Config{
			Host:        "https://api.example.com",
			BearerToken: "kubeconfig-static-token",
			// BearerTokenFile intentionally empty: out-of-cluster kubeconfig case.
		},
	}

	flags, err := r.clientForNamespace("ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *flags.BearerToken; got != "kubeconfig-static-token" {
		t.Errorf("expected fallback to the static token, got %q", got)
	}
}

func TestClientForNamespace_ErrorsWhenTokenFileUnreadable(t *testing.T) {
	r := &ConditionalTTLReconciler{
		Config: &rest.Config{
			BearerTokenFile: filepath.Join(t.TempDir(), "does-not-exist"),
		},
	}

	flags, err := r.clientForNamespace("ns")
	if err == nil {
		t.Fatal("expected an error for an unreadable token file, got nil")
	}
	if flags != nil {
		t.Errorf("expected nil ConfigFlags on error, got %#v", flags)
	}
}
