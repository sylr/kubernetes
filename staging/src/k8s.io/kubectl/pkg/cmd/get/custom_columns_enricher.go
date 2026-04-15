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
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/printers"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/jsonpath"
)

const customColumnsExtensionKey = "kubectl.kubernetes.io/custom-columns"

// CustomColumnSpec describes a single custom column to be added to a resource table.
type CustomColumnSpec struct {
	// Resource is the plural resource name (e.g. "nodes", "pods").
	Resource string `json:"resource"`
	// Name is the column header.
	Name string `json:"name"`
	// Label is the label key to extract. Mutually exclusive with Annotation.
	Label string `json:"label,omitempty"`
	// Annotation is the annotation key to extract. Mutually exclusive with Label and JSONPath.
	Annotation string `json:"annotation,omitempty"`
	// JSONPath is a jsonpath expression to evaluate against the row's embedded object.
	// Mutually exclusive with Label and Annotation. Requires full object inclusion.
	JSONPath string `json:"jsonpath,omitempty"`
	// OmitEmpty prints an empty string instead of "<none>" when the value is missing.
	OmitEmpty bool `json:"omitEmpty,omitempty"`
}

// customColumnsExtension is the deserialized form of the kubeconfig extension.
type customColumnsExtension struct {
	Columns []CustomColumnSpec `json:"columns"`
}

// CustomColumnsEnricher is a printer decorator that appends user-defined
// columns (from kubeconfig context extensions) to server-side tables by
// extracting values from each row's embedded object metadata.
type CustomColumnsEnricher struct {
	Delegate printers.ResourcePrinter
	Columns  []CustomColumnSpec
}

func (e *CustomColumnsEnricher) PrintObj(obj runtime.Object, writer io.Writer) error {
	table, ok := obj.(*metav1.Table)
	if !ok || len(e.Columns) == 0 {
		return e.Delegate.PrintObj(obj, writer)
	}

	// Append custom column definitions
	for _, col := range e.Columns {
		table.ColumnDefinitions = append(table.ColumnDefinitions, metav1.TableColumnDefinition{
			Name:        col.Name,
			Type:        "string",
			Description: fmt.Sprintf("Custom column from kubeconfig (%s)", col.source()),
		})
	}

	// Pre-parse any jsonpath expressions, warning on invalid ones.
	hasJSONPath := false
	parsers := make([]*jsonpath.JSONPath, len(e.Columns))
	for i, col := range e.Columns {
		if col.JSONPath != "" {
			expr, err := RelaxedJSONPathExpression(col.JSONPath)
			if err != nil {
				fmt.Fprintf(writer, "warning: invalid jsonpath %q for column %q: %v\n", col.JSONPath, col.Name, err)
				continue
			}
			p := jsonpath.New(col.Name).AllowMissingKeys(true)
			if err := p.Parse(expr); err != nil {
				fmt.Fprintf(writer, "warning: invalid jsonpath %q for column %q: %v\n", col.JSONPath, col.Name, err)
				continue
			}
			parsers[i] = p
			hasJSONPath = true
		}
	}

	// Append custom cell values to each row.
	// When jsonpath columns exist, unmarshal the row object once and reuse it.
	for i := range table.Rows {
		row := &table.Rows[i]
		meta := rowMetadata(row)
		var rowObj interface{}
		if hasJSONPath && row.Object.Raw != nil {
			json.Unmarshal(row.Object.Raw, &rowObj)
		}
		for j, col := range e.Columns {
			var val string
			switch {
			case col.Label != "":
				val = meta.Labels[col.Label]
			case col.Annotation != "":
				val = meta.Annotations[col.Annotation]
			case parsers[j] != nil && rowObj != nil:
				val = evalJSONPathObj(parsers[j], rowObj)
			}
			if val == "" && !col.OmitEmpty {
				val = "<none>"
			}
			row.Cells = append(row.Cells, val)
		}
	}

	return e.Delegate.PrintObj(table, writer)
}

func (c *CustomColumnSpec) source() string {
	if c.Label != "" {
		return "label:" + c.Label
	}
	if c.Annotation != "" {
		return "annotation:" + c.Annotation
	}
	return "jsonpath:" + c.JSONPath
}

// evalJSONPathObj evaluates a pre-parsed jsonpath expression against a
// pre-unmarshaled object and returns the result as a string.
func evalJSONPathObj(parser *jsonpath.JSONPath, obj interface{}) string {
	var buf bytes.Buffer
	if err := parser.Execute(&buf, obj); err != nil {
		return ""
	}
	return buf.String()
}

// HasJSONPathColumns returns true if any of the given columns use jsonpath expressions.
// If resource is non-empty, only columns matching that resource are considered.
func HasJSONPathColumns(columns []CustomColumnSpec, resource string) bool {
	for _, col := range columns {
		if col.JSONPath != "" && (resource == "" || col.Resource == resource) {
			return true
		}
	}
	return false
}

// rowMetadataResult holds extracted labels and annotations from a row's embedded object.
type rowMetadataResult struct {
	Labels      map[string]string
	Annotations map[string]string
}

// rowMetadata extracts labels and annotations from a table row's embedded object metadata.
func rowMetadata(row *metav1.TableRow) rowMetadataResult {
	if row.Object.Raw == nil {
		return rowMetadataResult{}
	}
	var obj struct {
		Metadata struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(row.Object.Raw, &obj); err != nil {
		return rowMetadataResult{}
	}
	return rowMetadataResult{
		Labels:      obj.Metadata.Labels,
		Annotations: obj.Metadata.Annotations,
	}
}

// loadCustomColumns reads custom column specs from the active kubeconfig
// context extension and returns those matching the given resource name.
func loadCustomColumns(loader interface{ RawConfig() (clientcmdapi.Config, error) }, resource string) []CustomColumnSpec {
	rawConfig, err := loader.RawConfig()
	if err != nil {
		return nil
	}

	ctx, ok := rawConfig.Contexts[rawConfig.CurrentContext]
	if !ok || ctx == nil {
		return nil
	}

	ext, ok := ctx.Extensions[customColumnsExtensionKey]
	if !ok || ext == nil {
		return nil
	}

	// The extension is stored as *runtime.Unknown with Raw JSON bytes.
	unknown, ok := ext.(*runtime.Unknown)
	if !ok || unknown == nil {
		return nil
	}

	var parsed customColumnsExtension
	if err := json.Unmarshal(unknown.Raw, &parsed); err != nil {
		return nil
	}

	var matched []CustomColumnSpec
	for _, col := range parsed.Columns {
		if col.Resource == resource && col.Name != "" && (col.Label != "" || col.Annotation != "" || col.JSONPath != "") {
			matched = append(matched, col)
		}
	}
	return matched
}
