/*
Copyright 2025 The Kubernetes Authors.

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

package get

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/printers"
)

func mustMarshalMetadata(labels, annotations map[string]string) runtime.RawExtension {
	meta := map[string]any{}
	if labels != nil {
		meta["labels"] = labels
	}
	if annotations != nil {
		meta["annotations"] = annotations
	}
	raw, _ := json.Marshal(map[string]any{"metadata": meta})
	return runtime.RawExtension{Raw: raw}
}

func TestCustomColumnsEnricher(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &CustomColumnsEnricher{
		Delegate: delegate,
		Columns: []CustomColumnSpec{
			{Resource: "nodes", Name: "Dedicated", Label: "formance.com/dedicated"},
			{Resource: "nodes", Name: "Team", Annotation: "team.company.com/owner"},
		},
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Status", Type: "string"},
		},
		Rows: []metav1.TableRow{
			{
				Cells: []any{"node-1", "Ready"},
				Object: mustMarshalMetadata(
					map[string]string{"formance.com/dedicated": "ledger"},
					map[string]string{"team.company.com/owner": "platform"},
				),
			},
			{
				Cells: []any{"node-2", "Ready"},
				Object: mustMarshalMetadata(
					map[string]string{"formance.com/dedicated": "payments"},
					nil,
				),
			},
			{
				Cells:  []any{"node-3", "Ready"},
				Object: mustMarshalMetadata(nil, nil),
			},
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	// Check columns
	wantCols := []string{"Name", "Status", "Dedicated", "Team"}
	if len(captured.ColumnDefinitions) != len(wantCols) {
		t.Fatalf("expected %d columns, got %d", len(wantCols), len(captured.ColumnDefinitions))
	}
	for i, want := range wantCols {
		if captured.ColumnDefinitions[i].Name != want {
			t.Errorf("column %d: expected %q, got %q", i, want, captured.ColumnDefinitions[i].Name)
		}
	}

	// Check cell values
	wantRows := []struct {
		dedicated, team string
	}{
		{"ledger", "platform"},
		{"payments", "<none>"},
		{"<none>", "<none>"},
	}
	for i, want := range wantRows {
		dedicated, _ := captured.Rows[i].Cells[2].(string)
		team, _ := captured.Rows[i].Cells[3].(string)
		if dedicated != want.dedicated {
			t.Errorf("row %d Dedicated: expected %q, got %q", i, want.dedicated, dedicated)
		}
		if team != want.team {
			t.Errorf("row %d Team: expected %q, got %q", i, want.team, team)
		}
	}
}

func TestCustomColumnsEnricherNoColumns(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &CustomColumnsEnricher{
		Delegate: delegate,
		Columns:  nil,
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
		},
		Rows: []metav1.TableRow{
			{Cells: []any{"node-1"}},
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	// Table should be unchanged
	if len(captured.ColumnDefinitions) != 1 {
		t.Errorf("expected 1 column, got %d", len(captured.ColumnDefinitions))
	}
	if len(captured.Rows[0].Cells) != 1 {
		t.Errorf("expected 1 cell, got %d", len(captured.Rows[0].Cells))
	}
}

func TestCustomColumnsEnricherNilRaw(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &CustomColumnsEnricher{
		Delegate: delegate,
		Columns: []CustomColumnSpec{
			{Resource: "nodes", Name: "Zone", Label: "topology.kubernetes.io/zone"},
		},
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
		},
		Rows: []metav1.TableRow{
			{Cells: []any{"node-1"}}, // no Object.Raw
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	zone, _ := captured.Rows[0].Cells[1].(string)
	if zone != "<none>" {
		t.Errorf("expected %q, got %q", "<none>", zone)
	}
}

func mustMarshalFullObject(obj map[string]any) runtime.RawExtension {
	raw, _ := json.Marshal(obj)
	return runtime.RawExtension{Raw: raw}
}

func TestCustomColumnsEnricherJSONPath(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &CustomColumnsEnricher{
		Delegate: delegate,
		Columns: []CustomColumnSpec{
			{Resource: "pods", Name: "Node", JSONPath: ".spec.nodeName"},
			{Resource: "pods", Name: "IP", JSONPath: ".status.podIP"},
			{Resource: "pods", Name: "Missing", JSONPath: ".spec.doesNotExist", OmitEmpty: true},
		},
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
		},
		Rows: []metav1.TableRow{
			{
				Cells: []any{"pod-1"},
				Object: mustMarshalFullObject(map[string]any{
					"spec":   map[string]any{"nodeName": "node-a"},
					"status": map[string]any{"podIP": "10.0.0.1"},
				}),
			},
			{
				Cells: []any{"pod-2"},
				Object: mustMarshalFullObject(map[string]any{
					"spec":   map[string]any{"nodeName": "node-b"},
					"status": map[string]any{},
				}),
			},
			{
				Cells:  []any{"pod-3"},
				Object: runtime.RawExtension{}, // nil Raw
			},
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	// Check columns
	wantCols := []string{"Name", "Node", "IP", "Missing"}
	if len(captured.ColumnDefinitions) != len(wantCols) {
		t.Fatalf("expected %d columns, got %d", len(wantCols), len(captured.ColumnDefinitions))
	}

	// Check values
	tests := []struct {
		row              int
		node, ip, missing string
	}{
		{0, "node-a", "10.0.0.1", ""},
		{1, "node-b", "<none>", ""},
		{2, "<none>", "<none>", ""},
	}
	for _, tt := range tests {
		node, _ := captured.Rows[tt.row].Cells[1].(string)
		ip, _ := captured.Rows[tt.row].Cells[2].(string)
		missing, _ := captured.Rows[tt.row].Cells[3].(string)
		if node != tt.node {
			t.Errorf("row %d Node: expected %q, got %q", tt.row, tt.node, node)
		}
		if ip != tt.ip {
			t.Errorf("row %d IP: expected %q, got %q", tt.row, tt.ip, ip)
		}
		if missing != tt.missing {
			t.Errorf("row %d Missing: expected %q, got %q", tt.row, tt.missing, missing)
		}
	}
}

func TestHasJSONPathColumns(t *testing.T) {
	if HasJSONPathColumns(nil, "") {
		t.Error("nil columns should return false")
	}
	if HasJSONPathColumns([]CustomColumnSpec{{Label: "foo"}}, "") {
		t.Error("label-only columns should return false")
	}
	if !HasJSONPathColumns([]CustomColumnSpec{{JSONPath: ".spec.nodeName"}}, "") {
		t.Error("jsonpath column should return true")
	}
	if HasJSONPathColumns([]CustomColumnSpec{{Resource: "pods", JSONPath: ".spec.nodeName"}}, "nodes") {
		t.Error("jsonpath column for different resource should return false")
	}
	if !HasJSONPathColumns([]CustomColumnSpec{{Resource: "pods", JSONPath: ".spec.nodeName"}}, "pods") {
		t.Error("jsonpath column for matching resource should return true")
	}
}
