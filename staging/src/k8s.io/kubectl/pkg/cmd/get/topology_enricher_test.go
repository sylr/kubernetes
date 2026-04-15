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

func TestTopologyEnricher(t *testing.T) {
	tests := []struct {
		name           string
		nodeZones      map[string]string
		columns        []metav1.TableColumnDefinition
		rows           []metav1.TableRow
		wantColumns    []string
		wantZoneCells  []string
		wantUnmodified bool
	}{
		{
			name: "adds zone column after node column",
			nodeZones: map[string]string{
				"node-1": "us-east-1a",
				"node-2": "us-east-1b",
			},
			columns: []metav1.TableColumnDefinition{
				{Name: "Name", Type: "string"},
				{Name: "Ready", Type: "string"},
				{Name: "Status", Type: "string"},
				{Name: "Restarts", Type: "string"},
				{Name: "Age", Type: "string"},
				{Name: "IP", Type: "string", Priority: 1},
				{Name: "Node", Type: "string", Priority: 1},
				{Name: "Nominated Node", Type: "string", Priority: 1},
				{Name: "Readiness Gates", Type: "string", Priority: 1},
			},
			rows: []metav1.TableRow{
				{Cells: []any{"pod-1", "1/1", "Running", "0", "1d", "10.0.0.1", "node-1", "<none>", "<none>"}},
				{Cells: []any{"pod-2", "1/1", "Running", "0", "2d", "10.0.0.2", "node-2", "<none>", "<none>"}},
			},
			wantColumns:   []string{"Name", "Ready", "Status", "Restarts", "Age", "IP", "Node", "Zone", "Nominated Node", "Readiness Gates"},
			wantZoneCells: []string{"us-east-1a", "us-east-1b"},
		},
		{
			name:      "node without zone label shows <none>",
			nodeZones: map[string]string{},
			columns: []metav1.TableColumnDefinition{
				{Name: "Name", Type: "string"},
				{Name: "Node", Type: "string", Priority: 1},
			},
			rows: []metav1.TableRow{
				{Cells: []any{"pod-1", "node-1"}},
			},
			wantColumns:   []string{"Name", "Node", "Zone"},
			wantZoneCells: []string{"<none>"},
		},
		{
			name: "unscheduled pod shows <none>",
			nodeZones: map[string]string{
				"node-1": "us-east-1a",
			},
			columns: []metav1.TableColumnDefinition{
				{Name: "Name", Type: "string"},
				{Name: "Node", Type: "string", Priority: 1},
			},
			rows: []metav1.TableRow{
				{Cells: []any{"pod-1", "<none>"}},
			},
			wantColumns:   []string{"Name", "Node", "Zone"},
			wantZoneCells: []string{"<none>"},
		},
		{
			name:      "table without node column is not modified",
			nodeZones: map[string]string{"node-1": "us-east-1a"},
			columns: []metav1.TableColumnDefinition{
				{Name: "Name", Type: "string"},
				{Name: "Status", Type: "string"},
			},
			rows: []metav1.TableRow{
				{Cells: []any{"svc-1", "Active"}},
			},
			wantUnmodified: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			var captured *metav1.Table

			delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
				captured = obj.(*metav1.Table)
				return nil
			})

			enricher := &TopologyEnricher{
				Delegate:  delegate,
				NodeZones: tt.nodeZones,
			}

			table := &metav1.Table{
				ColumnDefinitions: tt.columns,
				Rows:              tt.rows,
			}

			if err := enricher.PrintObj(table, &buf); err != nil {
				t.Fatalf("PrintObj returned error: %v", err)
			}

			if tt.wantUnmodified {
				if len(captured.ColumnDefinitions) != len(tt.columns) {
					t.Errorf("expected %d columns, got %d", len(tt.columns), len(captured.ColumnDefinitions))
				}
				return
			}

			// Check column names
			if len(captured.ColumnDefinitions) != len(tt.wantColumns) {
				t.Fatalf("expected %d columns, got %d", len(tt.wantColumns), len(captured.ColumnDefinitions))
			}
			for i, wantName := range tt.wantColumns {
				if captured.ColumnDefinitions[i].Name != wantName {
					t.Errorf("column %d: expected name %q, got %q", i, wantName, captured.ColumnDefinitions[i].Name)
				}
			}

			// Check zone column has Priority 1
			for _, col := range captured.ColumnDefinitions {
				if col.Name == "Zone" && col.Priority != 1 {
					t.Errorf("Zone column should have Priority 1, got %d", col.Priority)
				}
			}

			// Check zone cell values
			if len(captured.Rows) != len(tt.wantZoneCells) {
				t.Fatalf("expected %d rows, got %d", len(tt.wantZoneCells), len(captured.Rows))
			}

			// Find zone column index
			zoneIdx := -1
			for i, col := range captured.ColumnDefinitions {
				if col.Name == "Zone" {
					zoneIdx = i
					break
				}
			}
			if zoneIdx < 0 {
				t.Fatal("Zone column not found")
			}

			for i, wantZone := range tt.wantZoneCells {
				gotZone, ok := captured.Rows[i].Cells[zoneIdx].(string)
				if !ok {
					t.Errorf("row %d: zone cell is not a string", i)
					continue
				}
				if gotZone != wantZone {
					t.Errorf("row %d: expected zone %q, got %q", i, wantZone, gotZone)
				}
			}

			// Check total cell count matches column count
			for i, row := range captured.Rows {
				if len(row.Cells) != len(captured.ColumnDefinitions) {
					t.Errorf("row %d: expected %d cells, got %d", i, len(captured.ColumnDefinitions), len(row.Cells))
				}
			}
		})
	}
}

func mustMarshalOwnerRefs(ownerName string) runtime.RawExtension {
	obj := map[string]any{
		"metadata": map[string]any{
			"ownerReferences": []map[string]any{
				{"name": ownerName},
			},
		},
	}
	raw, _ := json.Marshal(obj)
	return runtime.RawExtension{Raw: raw}
}

func TestTopologyEnricherSorting(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &TopologyEnricher{
		Delegate: delegate,
		NodeZones: map[string]string{
			"node-1": "us-east-1b",
			"node-2": "us-east-1a",
			"node-3": "us-east-1a",
		},
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Node", Type: "string", Priority: 1},
		},
		Rows: []metav1.TableRow{
			// owner-b, zone us-east-1b
			{Cells: []any{"pod-z", "node-1"}, Object: mustMarshalOwnerRefs("deploy-b")},
			// owner-a, zone us-east-1a
			{Cells: []any{"pod-b", "node-2"}, Object: mustMarshalOwnerRefs("deploy-a")},
			// owner-a, zone us-east-1a
			{Cells: []any{"pod-a", "node-3"}, Object: mustMarshalOwnerRefs("deploy-a")},
			// owner-a, zone us-east-1b
			{Cells: []any{"pod-c", "node-1"}, Object: mustMarshalOwnerRefs("deploy-a")},
			// owner-b, zone us-east-1a
			{Cells: []any{"pod-d", "node-2"}, Object: mustMarshalOwnerRefs("deploy-b")},
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	// Expected order: deploy-a/us-east-1a/pod-a, deploy-a/us-east-1a/pod-b,
	// deploy-a/us-east-1b/pod-c, deploy-b/us-east-1a/pod-d, deploy-b/us-east-1b/pod-z
	wantNames := []string{"pod-a", "pod-b", "pod-c", "pod-d", "pod-z"}
	if len(captured.Rows) != len(wantNames) {
		t.Fatalf("expected %d rows, got %d", len(wantNames), len(captured.Rows))
	}
	for i, want := range wantNames {
		got, _ := captured.Rows[i].Cells[0].(string)
		if got != want {
			t.Errorf("row %d: expected pod name %q, got %q", i, want, got)
		}
	}
}

func TestTopologyEnricherSortingEdgeCases(t *testing.T) {
	var buf bytes.Buffer
	var captured *metav1.Table

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		captured = obj.(*metav1.Table)
		return nil
	})

	enricher := &TopologyEnricher{
		Delegate: delegate,
		NodeZones: map[string]string{
			"node-1": "us-east-1a",
			"node-2": "us-east-1b",
		},
	}

	// Mix of: no Object.Raw, no ownerReferences, normal owner ref
	noRaw := runtime.RawExtension{}
	noOwner := func() runtime.RawExtension {
		raw, _ := json.Marshal(map[string]any{"metadata": map[string]any{}})
		return runtime.RawExtension{Raw: raw}
	}

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Node", Type: "string", Priority: 1},
		},
		Rows: []metav1.TableRow{
			{Cells: []any{"owned-pod", "node-2"}, Object: mustMarshalOwnerRefs("deploy-a")},
			{Cells: []any{"orphan-b", "node-1"}, Object: noOwner()},
			{Cells: []any{"no-raw", "node-2"}, Object: noRaw},
			{Cells: []any{"orphan-a", "node-2"}, Object: noOwner()},
		},
	}

	if err := enricher.PrintObj(table, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}

	// Empty owner (no-raw, orphan-a, orphan-b) sorts before "deploy-a",
	// then within empty-owner group: sorted by zone then name.
	// no-raw → node-2 → us-east-1b, orphan-a → node-2 → us-east-1b, orphan-b → node-1 → us-east-1a
	// So: orphan-b (1a), no-raw (1b), orphan-a (1b), then owned-pod (deploy-a, 1b)
	wantNames := []string{"orphan-b", "no-raw", "orphan-a", "owned-pod"}
	if len(captured.Rows) != len(wantNames) {
		t.Fatalf("expected %d rows, got %d", len(wantNames), len(captured.Rows))
	}
	for i, want := range wantNames {
		got, _ := captured.Rows[i].Cells[0].(string)
		if got != want {
			t.Errorf("row %d: expected pod name %q, got %q", i, want, got)
		}
	}
}

func TestTopologyEnricherNonTable(t *testing.T) {
	var buf bytes.Buffer
	var called bool

	delegate := printers.ResourcePrinterFunc(func(obj runtime.Object, w io.Writer) error {
		called = true
		return nil
	})

	enricher := &TopologyEnricher{
		Delegate:  delegate,
		NodeZones: map[string]string{"node-1": "zone-a"},
	}

	// Pass a non-table object
	pod := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	if err := enricher.PrintObj(pod, &buf); err != nil {
		t.Fatalf("PrintObj returned error: %v", err)
	}
	if !called {
		t.Error("delegate was not called for non-table object")
	}
}
