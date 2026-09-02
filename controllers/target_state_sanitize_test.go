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
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	cleanerv1alpha1 "github.com/vtex/cleaner-controller/api/v1alpha1"
)

func objectWithManagedFields(name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name": name,
			"managedFields": []interface{}{
				map[string]interface{}{"manager": "some-controller", "operation": "Update"},
			},
		},
	}
}

func TestSanitizeTargetState_Nil(t *testing.T) {
	if got := sanitizeTargetState(nil); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestSanitizeTargetState_SingleObject_StripsManagedFields(t *testing.T) {
	original := &unstructured.Unstructured{Object: objectWithManagedFields("my-object")}

	sanitized := sanitizeTargetState(original)

	metadata, ok := sanitized.Object["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata map, got %#v", sanitized.Object["metadata"])
	}
	if _, present := metadata["managedFields"]; present {
		t.Errorf("expected managedFields to be stripped, still present: %#v", metadata["managedFields"])
	}
	if metadata["name"] != "my-object" {
		t.Errorf("expected unrelated fields to be preserved, name = %#v", metadata["name"])
	}

	// the input must not be mutated: sanitize is applied to persisted status only,
	// the original is also used as-is for in-memory CEL evaluation.
	originalMetadata := original.Object["metadata"].(map[string]interface{})
	if _, present := originalMetadata["managedFields"]; !present {
		t.Errorf("input object was mutated: managedFields removed from the original")
	}
}

func TestSanitizeTargetState_SmallList_NotTruncated(t *testing.T) {
	items := make([]interface{}, 5)
	for i := range items {
		items[i] = objectWithManagedFields(fmt.Sprintf("item-%d", i))
	}
	list := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMapList",
		"items":      items,
	}}

	sanitized := sanitizeTargetState(list)

	gotItems, found, err := unstructured.NestedSlice(sanitized.Object, "items")
	if err != nil || !found {
		t.Fatalf("expected items field, found=%v err=%v", found, err)
	}
	if len(gotItems) != 5 {
		t.Errorf("expected all 5 items preserved, got %d", len(gotItems))
	}
	if _, present := sanitized.Object["truncatedItemCount"]; present {
		t.Errorf("did not expect truncatedItemCount on a list under the cap")
	}
	for i, item := range gotItems {
		obj, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("item %d: expected map, got %#v", i, item)
		}
		metadata := obj["metadata"].(map[string]interface{})
		if _, present := metadata["managedFields"]; present {
			t.Errorf("item %d: expected managedFields to be stripped", i)
		}
	}
}

func TestSanitizeTargetState_LargeList_TruncatedWithCount(t *testing.T) {
	const totalItems = maxPersistedTargetListItems + 5
	items := make([]interface{}, totalItems)
	for i := range items {
		items[i] = objectWithManagedFields(fmt.Sprintf("item-%d", i))
	}
	list := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMapList",
		"items":      items,
	}}

	sanitized := sanitizeTargetState(list)

	gotItems, found, err := unstructured.NestedSlice(sanitized.Object, "items")
	if err != nil || !found {
		t.Fatalf("expected items field, found=%v err=%v", found, err)
	}
	if len(gotItems) != maxPersistedTargetListItems {
		t.Errorf("expected items capped to %d, got %d", maxPersistedTargetListItems, len(gotItems))
	}
	count, present := sanitized.Object["truncatedItemCount"]
	if !present {
		t.Fatalf("expected truncatedItemCount to be set on a truncated list")
	}
	if count != totalItems {
		t.Errorf("expected truncatedItemCount = %d, got %v", totalItems, count)
	}

	// the input list must be left fully intact — a caller relying on the
	// original (e.g. CEL evaluation, which runs before this is called) must
	// still see every matched item.
	originalItems := list.Object["items"].([]interface{})
	if len(originalItems) != totalItems {
		t.Errorf("input list was mutated: expected %d items, got %d", totalItems, len(originalItems))
	}
}

func TestSanitizeTargetStatusesForPersistence(t *testing.T) {
	ts := []cleanerv1alpha1.TargetStatus{
		{
			Name:   "target-with-nil-state",
			Delete: true,
			State:  nil,
		},
		{
			Name:   "target-with-object",
			Delete: false,
			State:  &unstructured.Unstructured{Object: objectWithManagedFields("obj")},
		},
	}

	sanitized := sanitizeTargetStatusesForPersistence(ts)

	if len(sanitized) != len(ts) {
		t.Fatalf("expected %d entries, got %d", len(ts), len(sanitized))
	}
	if sanitized[0].State != nil {
		t.Errorf("expected nil State to remain nil, got %#v", sanitized[0].State)
	}
	if sanitized[0].Name != "target-with-nil-state" || sanitized[0].Delete != true {
		t.Errorf("expected non-State fields to be preserved verbatim, got %#v", sanitized[0])
	}
	metadata := sanitized[1].State.Object["metadata"].(map[string]interface{})
	if _, present := metadata["managedFields"]; present {
		t.Errorf("expected managedFields stripped on entry 1")
	}
}
