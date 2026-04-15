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
	"context"
	"encoding/json"
	"io"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/kubernetes"
)

const topologyZoneLabel = "topology.kubernetes.io/zone"

// TopologyEnricher is a printer decorator that adds a "Zone" column to pod
// tables by looking up the topology.kubernetes.io/zone label on the node each
// pod is scheduled on.
type TopologyEnricher struct {
	Delegate  printers.ResourcePrinter
	NodeZones map[string]string
}

func (e *TopologyEnricher) PrintObj(obj runtime.Object, writer io.Writer) error {
	table, ok := obj.(*metav1.Table)
	if !ok {
		return e.Delegate.PrintObj(obj, writer)
	}

	// Find the "Node" column index
	nodeColIdx := -1
	for i, col := range table.ColumnDefinitions {
		if col.Name == "Node" {
			nodeColIdx = i
			break
		}
	}

	if nodeColIdx >= 0 {
		insertIdx := nodeColIdx + 1

		// Insert "Zone" column right after "Node"
		zoneCol := metav1.TableColumnDefinition{
			Name:        "Zone",
			Type:        "string",
			Priority:    1,
			Description: "The topology zone of the node the pod is scheduled on.",
		}
		newCols := make([]metav1.TableColumnDefinition, 0, len(table.ColumnDefinitions)+1)
		newCols = append(newCols, table.ColumnDefinitions[:insertIdx]...)
		newCols = append(newCols, zoneCol)
		newCols = append(newCols, table.ColumnDefinitions[insertIdx:]...)
		table.ColumnDefinitions = newCols

		// Insert zone cell into each row
		for i := range table.Rows {
			row := &table.Rows[i]
			zone := "<none>"
			if nodeColIdx < len(row.Cells) {
				if nodeName, ok := row.Cells[nodeColIdx].(string); ok && nodeName != "" && nodeName != "<none>" {
					if z, found := e.NodeZones[nodeName]; found && z != "" {
						zone = z
					}
				}
			}
			newCells := make([]interface{}, 0, len(row.Cells)+1)
			newCells = append(newCells, row.Cells[:insertIdx]...)
			newCells = append(newCells, zone)
			newCells = append(newCells, row.Cells[insertIdx:]...)
			row.Cells = newCells
		}

		// Sort rows by owner reference name, then zone, then pod name.
		sortTableRows(table.Rows, insertIdx)
	}

	return e.Delegate.PrintObj(table, writer)
}

// sortTableRows sorts table rows by owner reference name, then by zone, then
// by pod name (first cell). Owner references are extracted once per row to
// avoid repeated JSON unmarshaling inside the comparator.
func sortTableRows(rows []metav1.TableRow, zoneColIdx int) {
	n := len(rows)
	owners := make([]string, n)
	for i := range rows {
		owners[i] = ownerRefName(rows[i])
	}

	// Sort an index slice so the cached owners stay aligned with rows.
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}

	sort.SliceStable(indices, func(i, j int) bool {
		ii, jj := indices[i], indices[j]
		if owners[ii] != owners[jj] {
			return owners[ii] < owners[jj]
		}

		zoneI := cellString(rows[ii].Cells, zoneColIdx)
		zoneJ := cellString(rows[jj].Cells, zoneColIdx)
		if zoneI != zoneJ {
			return zoneI < zoneJ
		}

		nameI := cellString(rows[ii].Cells, 0)
		nameJ := cellString(rows[jj].Cells, 0)
		return nameI < nameJ
	})

	sorted := make([]metav1.TableRow, n)
	for i, idx := range indices {
		sorted[i] = rows[idx]
	}
	copy(rows, sorted)
}

// ownerRefName extracts the first owner reference name from a table row's
// embedded object metadata.
func ownerRefName(row metav1.TableRow) string {
	if row.Object.Raw == nil {
		return ""
	}
	var obj struct {
		Metadata struct {
			OwnerReferences []struct {
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(row.Object.Raw, &obj); err != nil {
		return ""
	}
	if len(obj.Metadata.OwnerReferences) > 0 {
		return obj.Metadata.OwnerReferences[0].Name
	}
	return ""
}

// cellString returns the string value of a cell at the given index, or empty
// string if out of bounds or not a string.
func cellString(cells []interface{}, idx int) string {
	if idx < 0 || idx >= len(cells) {
		return ""
	}
	if s, ok := cells[idx].(string); ok {
		return s
	}
	return ""
}

// NodeTopologyEnricher is a printer decorator that adds a "Zone" column to
// node tables by extracting the topology.kubernetes.io/zone label from each
// row's embedded object metadata, and sorts nodes by zone then name.
type NodeTopologyEnricher struct {
	Delegate printers.ResourcePrinter
}

func (e *NodeTopologyEnricher) PrintObj(obj runtime.Object, writer io.Writer) error {
	table, ok := obj.(*metav1.Table)
	if !ok {
		return e.Delegate.PrintObj(obj, writer)
	}

	// Find the "Version" column to insert "Zone" after it.
	versionColIdx := -1
	for i, col := range table.ColumnDefinitions {
		if col.Name == "Version" {
			versionColIdx = i
			break
		}
	}

	if versionColIdx >= 0 {
		insertIdx := versionColIdx + 1

		// Insert "Zone" column right after "Version"
		zoneCol := metav1.TableColumnDefinition{
			Name:        "Zone",
			Type:        "string",
			Description: "The topology zone of the node.",
		}
		newCols := make([]metav1.TableColumnDefinition, 0, len(table.ColumnDefinitions)+1)
		newCols = append(newCols, table.ColumnDefinitions[:insertIdx]...)
		newCols = append(newCols, zoneCol)
		newCols = append(newCols, table.ColumnDefinitions[insertIdx:]...)
		table.ColumnDefinitions = newCols

		// Insert zone cell into each row, extracting from embedded labels
		for i := range table.Rows {
			row := &table.Rows[i]
			zone := rowLabel(row, topologyZoneLabel)
			if zone == "" {
				zone = "<none>"
			}
			newCells := make([]interface{}, 0, len(row.Cells)+1)
			newCells = append(newCells, row.Cells[:insertIdx]...)
			newCells = append(newCells, zone)
			newCells = append(newCells, row.Cells[insertIdx:]...)
			row.Cells = newCells
		}

		// Sort nodes by zone then name.
		sort.SliceStable(table.Rows, func(i, j int) bool {
			zoneI := cellString(table.Rows[i].Cells, insertIdx)
			zoneJ := cellString(table.Rows[j].Cells, insertIdx)
			if zoneI != zoneJ {
				return zoneI < zoneJ
			}
			nameI := cellString(table.Rows[i].Cells, 0)
			nameJ := cellString(table.Rows[j].Cells, 0)
			return nameI < nameJ
		})
	}

	return e.Delegate.PrintObj(table, writer)
}

// rowLabel extracts a label value from a table row's embedded object metadata.
func rowLabel(row *metav1.TableRow, label string) string {
	if row.Object.Raw == nil {
		return ""
	}
	var obj struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(row.Object.Raw, &obj); err != nil {
		return ""
	}
	return obj.Metadata.Labels[label]
}

// fetchNodeZones fetches all nodes and returns a map of node name to topology zone.
func fetchNodeZones(clientset kubernetes.Interface) (map[string]string, error) {
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	zones := make(map[string]string, len(nodes.Items))
	for _, node := range nodes.Items {
		if zone, ok := node.Labels[topologyZoneLabel]; ok {
			zones[node.Name] = zone
		}
	}
	return zones, nil
}
