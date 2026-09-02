package controllers

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newTestService() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{}}
}

func TestIsExcluded(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{name: "no annotations", annotations: nil, want: false},
		{name: "excluded true", annotations: map[string]string{idleAnnotationExclude: "true"}, want: true},
		{name: "excluded false string", annotations: map[string]string{idleAnnotationExclude: "false"}, want: false},
		{name: "unrelated annotation", annotations: map[string]string{"foo": "bar"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService()
			if tc.annotations != nil {
				svc.SetAnnotations(tc.annotations)
			}
			if got := isExcluded(svc); got != tc.want {
				t.Errorf("isExcluded() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasMinScaleZero(t *testing.T) {
	cases := []struct {
		name         string
		templateAnns map[string]string
		skipSet      bool
		want         bool
	}{
		{name: "min-scale zero", templateAnns: map[string]string{knativeMinScaleAnnotation: "0"}, want: true},
		{name: "min-scale one", templateAnns: map[string]string{knativeMinScaleAnnotation: "1"}, want: false},
		{name: "no min-scale annotation", templateAnns: map[string]string{"other": "x"}, want: false},
		{name: "no template annotations at all", skipSet: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService()
			if !tc.skipSet {
				err := unstructured.SetNestedStringMap(svc.Object, tc.templateAnns, "spec", "template", "metadata", "annotations")
				if err != nil {
					t.Fatalf("SetNestedStringMap() error = %v", err)
				}
			}
			got, err := hasMinScaleZero(svc)
			if err != nil {
				t.Fatalf("hasMinScaleZero() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("hasMinScaleZero() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadIdleSince(t *testing.T) {
	fixed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	t.Run("annotation absent", func(t *testing.T) {
		svc := newTestService()
		_, ok, err := readIdleSince(svc)
		if err != nil {
			t.Fatalf("readIdleSince() error = %v", err)
		}
		if ok {
			t.Errorf("readIdleSince() ok = true, want false")
		}
	})

	t.Run("valid RFC3339 annotation", func(t *testing.T) {
		svc := newTestService()
		svc.SetAnnotations(map[string]string{idleAnnotationIdleSince: fixed.Format(time.RFC3339)})
		got, ok, err := readIdleSince(svc)
		if err != nil {
			t.Fatalf("readIdleSince() error = %v", err)
		}
		if !ok {
			t.Fatalf("readIdleSince() ok = false, want true")
		}
		if !got.Equal(fixed) {
			t.Errorf("readIdleSince() = %v, want %v", got, fixed)
		}
	})

	t.Run("malformed annotation returns error", func(t *testing.T) {
		svc := newTestService()
		svc.SetAnnotations(map[string]string{idleAnnotationIdleSince: "not-a-timestamp"})
		_, _, err := readIdleSince(svc)
		if err == nil {
			t.Fatalf("readIdleSince() error = nil, want error for malformed timestamp")
		}
	})
}
