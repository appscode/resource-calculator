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

package cmds

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	core_util "kmodules.xyz/client-go/core/v1"
	"kmodules.xyz/client-go/tools/parser"
	"kubedb.dev/apimachinery/apis/kubedb"
	cs "kubedb.dev/apimachinery/client/clientset/versioned"
)

func NewCmdCheckDeprecated(clientGetter genericclioptions.RESTClientGetter) *cobra.Command {
	var local bool
	var dir string
	cmd := &cobra.Command{
		Use:                   "check-deprecated",
		Short:                 "Check for deprecated resources",
		DisableFlagsInUseLine: true,
		DisableAutoGenTag:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return checkDeprecated(clientGetter, local, dir)
		},
	}
	cmd.Flags().BoolVar(&local, "local", local, "Use local folder for processing resources")
	cmd.Flags().StringVarP(&dir, "dir", "f", dir, "File or folder to look for local resources")

	return cmd
}

func checkDeprecated(clientGetter genericclioptions.RESTClientGetter, local bool, filename string) error {
	cfg, err := clientGetter.ToRESTConfig()
	if err != nil {
		return err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	mapper, err := clientGetter.ToRESTMapper()
	if err != nil {
		return err
	}
	topology, err := core_util.DetectTopology(context.TODO(), metadata.NewForConfigOrDie(cfg))
	if err != nil {
		return err
	}
	kubedbclient, err := cs.NewForConfig(cfg)
	if err != nil {
		return err
	}

	ns, err := dc.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}).Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		return err
	}
	clusterID, _, err := unstructured.NestedString(ns.UnstructuredContent(), "metadata", "uid")
	if err != nil {
		return err
	}

	catalogmap, err := LoadCatalog(kubedbclient, local)
	if err != nil {
		return err
	}

	rsmap := map[schema.GroupVersionKind][]parser.ResourceInfo{}
	if local {
		objs, err := parser.ListPathResources(filename)
		if err != nil {
			return err
		}
		for _, item := range objs {
			gvk := item.Object.GroupVersionKind()
			if gvk.Group != kubedb.GroupName {
				continue
			}
			if gvk.Version == "v1alpha1" {
				content, err := Convert_kubedb_v1alpha1_To_v1alpha2(*item.Object, catalogmap, topology)
				if err != nil {
					return err
				}
				rsmap[gvk] = append(rsmap[gvk], parser.ResourceInfo{
					Filename: item.Filename,
					Object:   &unstructured.Unstructured{Object: content},
				})
			} else {
				rsmap[gvk] = append(rsmap[gvk], item)
			}
		}
	} else {
		for _, gvk := range registeredKubeDBTypes {
			mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if meta.IsNoMatchError(err) {
				continue
			} else if err != nil {
				return err
			}

			ri := dc.Resource(mapping.Resource).Namespace(core.NamespaceAll)
			if result, err := ri.List(context.TODO(), metav1.ListOptions{}); err != nil {
				return err
			} else {
				objects := make([]parser.ResourceInfo, 0, len(result.Items))
				for i, item := range result.Items {
					if gvk.Group == kubedb.GroupName && gvk.Version == "v1alpha1" {
						content, err := Convert_kubedb_v1alpha1_To_v1alpha2(item, catalogmap, topology)
						if err != nil {
							return err
						}
						objects = append(objects, parser.ResourceInfo{
							Filename: "",
							Object:   &unstructured.Unstructured{Object: content},
						})
					} else {
						objects = append(objects, parser.ResourceInfo{
							Filename: "",
							Object:   &result.Items[i],
						})
					}
				}
				rsmap[gvk] = objects
			}
		}
	}

	type Row struct {
		Filename  string
		Kind      string
		Namespace string
		Name      string
		Version   string
		Comment   string
	}
	var rows []Row
	for gvk, resources := range rsmap {
		for _, ri := range resources {
			name, _, err := unstructured.NestedString(ri.Object.Object, "metadata", "name")
			if err != nil {
				return err
			}
			namespace, _, err := unstructured.NestedString(ri.Object.Object, "metadata", "namespace")
			if err != nil {
				return err
			}
			version, _, err := unstructured.NestedString(ri.Object.Object, "spec", "version")
			if err != nil {
				return err
			}

			if versionInfo, ok := catalogmap[KindVersion{
				Kind:    gvk.Kind,
				Version: version,
			}]; !ok {
				rows = append(rows, Row{
					Filename:  ri.Filename,
					Kind:      gvk.Kind,
					Namespace: namespace,
					Name:      name,
					Version:   version,
					Comment:   "CUSTOM",
				})
			} else {
				vi, err := runtime.DefaultUnstructuredConverter.ToUnstructured(versionInfo)
				if err != nil {
					return err
				}
				deprecated, found, err := unstructured.NestedBool(vi, "spec", "deprecated")
				if err != nil {
					return err
				}
				if found && deprecated {
					rows = append(rows, Row{
						Filename:  ri.Filename,
						Kind:      gvk.Kind,
						Namespace: namespace,
						Name:      name,
						Version:   version,
						Comment:   "DEPRECATED",
					})
				}
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Filename != rows[j].Filename {
			return rows[i].Filename < rows[j].Filename
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Version < rows[j].Version
	})

	const padding = 3
	w := tabwriter.NewWriter(os.Stdout, 0, 0, padding, ' ', tabwriter.TabIndent)
	_, _ = fmt.Fprintln(os.Stdout, "")
	_, _ = fmt.Fprintf(os.Stdout, "CLUSTER ID: %s\n", clusterID)
	_, _ = fmt.Fprintln(os.Stdout, "")
	_, _ = fmt.Fprintln(w, "FILE\tKIND\tNAMESPACE\tNAME\tVERSION\tCOMMENT\t")
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t\n", row.Filename, row.Kind, row.Namespace, row.Name, row.Version, row.Comment)
	}
	return w.Flush()
}
