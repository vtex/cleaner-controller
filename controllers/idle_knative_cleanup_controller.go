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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	idleAnnotationExclude     = "cleaner.vtex.io/exclude"
	idleAnnotationIdleSince   = "cleaner.vtex.io/idle-since"
	knativeMinScaleAnnotation = "autoscaling.knative.dev/min-scale"
	knativeServiceLabel       = "serving.knative.dev/service"
)

// isExcluded reports whether the Service opted out of idle cleanup via the
// cleaner.vtex.io/exclude annotation.
func isExcluded(svc *unstructured.Unstructured) bool {
	return svc.GetAnnotations()[idleAnnotationExclude] == "true"
}

// hasMinScaleZero reports whether the Service's revision template declares
// autoscaling.knative.dev/min-scale: "0".
func hasMinScaleZero(svc *unstructured.Unstructured) (bool, error) {
	anns, found, err := unstructured.NestedStringMap(svc.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return false, fmt.Errorf("reading spec.template.metadata.annotations: %w", err)
	}
	if !found {
		return false, nil
	}
	return anns[knativeMinScaleAnnotation] == "0", nil
}

// readIdleSince reads and parses the cleaner.vtex.io/idle-since annotation.
// ok is false when the annotation is absent.
func readIdleSince(svc *unstructured.Unstructured) (time.Time, bool, error) {
	val, ok := svc.GetAnnotations()[idleAnnotationIdleSince]
	if !ok {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing %s annotation %q: %w", idleAnnotationIdleSince, val, err)
	}
	return t, true, nil
}
