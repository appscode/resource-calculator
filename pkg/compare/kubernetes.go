/*
Copyright AppsCode Inc. and Contributors

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

package compare

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProviderKubernetes labels databases discovered from in-cluster operator CRs.
const ProviderKubernetes Provider = "k8s"

// DiscoverOperators scans a Kubernetes cluster for databases managed by the
// alternative operators in operatorDescriptors(). It uses the controller-runtime
// client with unstructured objects, so it detects each operator purely by the
// presence of its CRD (GVK) and never imports those projects. Operators whose
// CRDs are absent are skipped silently. If namespace is non-empty the scan is
// limited to that namespace, otherwise it covers the whole cluster.
func DiscoverOperators(ctx context.Context, cfg *rest.Config, namespace string) ([]ManagedDatabase, []string, error) {
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes client: %w", err)
	}

	var listOpts []client.ListOption
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, d := range operatorDescriptors() {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: d.Group, Version: d.Version, Kind: d.Kind + "List"})
		if err := cl.List(ctx, list, listOpts...); err != nil {
			if operatorNotInstalled(err) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s (%s.%s): %v", d.Operator, strings.ToLower(d.Kind), d.Group, firstLine(err.Error())))
			continue
		}
		for i := range list.Items {
			item := list.Items[i]
			for _, c := range d.Extract(item.Object) {
				name := item.GetName()
				if c.Component != "" {
					name = name + " (" + c.Component + ")"
				}
				db := ManagedDatabase{
					Provider:         ProviderKubernetes,
					Service:          d.Operator,
					Engine:           d.Engine,
					Name:             name,
					Account:          item.GetNamespace(),
					Region:           item.GetNamespace(),
					MemoryGiBPerNode: c.MemGiB,
					NodeCount:        c.Replicas,
					Notes:            c.Note,
				}
				out = append(out, db)
			}
		}
	}
	return out, warnings, nil
}

// operatorNotInstalled reports whether a List error means the operator's CRD is
// simply absent (so the operator is not installed) rather than a real failure.
func operatorNotInstalled(err error) bool {
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no matches for kind") ||
		strings.Contains(msg, "the server could not find the requested resource")
}

// DiscoverImageWorkloads scans StatefulSets and Deployments for database
// containers that use Bitnami, Chainguard or Docker Hardened Images, sizing each
// by its memory limit/request times replicas. It complements DiscoverOperators
// (which covers operator-managed CRs); together they cover both ways databases
// are self-hosted on Kubernetes. Only those three image families are matched, so
// operator-managed and upstream-official images are not double counted.
func DiscoverImageWorkloads(ctx context.Context, cfg *rest.Config, namespace string) ([]ManagedDatabase, []string, error) {
	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes client: %w", err)
	}
	var listOpts []client.ListOption
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	var (
		out      []ManagedDatabase
		warnings []string
	)
	for _, kind := range []string{"StatefulSet", "Deployment"} {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: kind + "List"})
		if err := cl.List(ctx, list, listOpts...); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", kind, firstLine(err.Error())))
			continue
		}
		for i := range list.Items {
			item := list.Items[i]
			obj := item.Object
			replicas := atLeastOne(intAt(obj, "spec", "replicas"))
			for _, c := range sliceAt(obj, "spec", "template", "spec", "containers") {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				vendor, engine, ok := classifyDBImage(strAt(cm, "image"))
				if !ok {
					continue
				}
				mem, memOK := memAt(cm, "resources")
				db := ManagedDatabase{
					Provider:         ProviderKubernetes,
					Service:          vendor,
					Engine:           engine,
					Name:             item.GetName(),
					Account:          item.GetNamespace(),
					Region:           item.GetNamespace(),
					NodeType:         imageBase(strAt(cm, "image")),
					MemoryGiBPerNode: mem,
					NodeCount:        replicas,
				}
				if !memOK {
					db.Notes = "memory limit/request not set on the workload"
				}
				out = append(out, db)
				break // count one database container per workload
			}
		}
	}
	return out, warnings, nil
}
