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
	"io"

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
	}

	return e.Delegate.PrintObj(table, writer)
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
