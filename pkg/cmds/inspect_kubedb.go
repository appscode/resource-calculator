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
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"kmodules.xyz/apiversion"
	du "kmodules.xyz/client-go/dynamic"
	resourcemetrics "kmodules.xyz/resource-metrics"
	"kmodules.xyz/resource-metrics/api"
	"sigs.k8s.io/yaml"
)

type ResourceItem struct {
	APIVersion  string            `json:"apiVersion"`
	Group       string            `json:"group"`
	Kind        string            `json:"kind"`
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	UID         types.UID         `json:"uid"`
	Age         string            `json:"age"`
	MemoryLimit resource.Quantity `json:"memoryLimit"`
}

type ClusterResourceList struct {
	ClusterID string         `json:"clusterID"`
	Context   string         `json:"context,omitempty"`
	Items     []ResourceItem `json:"items"`
}

// newInspectKubeDBCmd lists KubeDB-managed (and other registered) resources in
// the cluster with their memory limits, one row per object. It walks the same
// GVK set as `calculate` (every kind registered in kmodules.xyz/resource-metrics,
// highest available API version per GroupKind) and uses the persistent
// `-o/--output` flag from `inspect` for the format.
func newInspectKubeDBCmd(clientGetter genericclioptions.RESTClientGetter, f *inspectFlags) *cobra.Command {
	var (
		apiGroups   []string
		allClusters bool
	)
	cmd := &cobra.Command{
		Use:   "kubedb",
		Short: "Inspect KubeDB-managed resources in the cluster (one row per object, with memory limit)",
		Long: `List resources in the current cluster that are registered in
kmodules.xyz/resource-metrics (KubeDB databases plus core/apps/batch workloads),
one row per object, with group/kind, namespace, name, UID, age and memory limit.

Walks the same GVK set as ` + "`calculate`" + ` (highest available API version per
GroupKind via kmodules.xyz/apiversion) but emits per-object rows instead of
per-kind totals. Use --apiGroups to filter and --all to sweep every kubeconfig
context. Output format follows the inherited -o/--output flag.`,
		DisableAutoGenTag:     true,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			groups := sets.New(apiGroups...)

			kubecfg, err := clientGetter.ToRawKubeConfigLoader().RawConfig()
			if err != nil {
				return err
			}

			var clusters []*ClusterResourceList
			if allClusters {
				for ctx := range kubecfg.Contexts {
					cfg, err := clientcmd.NewNonInteractiveClientConfig(kubecfg, ctx, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
					if err != nil {
						return err
					}
					out, err := listResources(ctx, cfg, groups)
					if err != nil {
						return err
					}
					clusters = append(clusters, out)
				}
			} else {
				cfg, err := clientGetter.ToRESTConfig()
				if err != nil {
					return err
				}
				out, err := listResources(kubecfg.CurrentContext, cfg, groups)
				if err != nil {
					return err
				}
				clusters = append(clusters, out)
			}

			switch f.output {
			case "json":
				data, err := json.MarshalIndent(clusters, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			case "yaml", "yml":
				data, err := yaml.Marshal(clusters)
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				printListTable(clusters)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&apiGroups, "apiGroups", apiGroups, "api groups for which to list resources")
	cmd.Flags().BoolVar(&allClusters, "all", allClusters, "If true, lists resources for all contexts in KUBECONFIG")

	return cmd
}

func listResources(ctxName string, cfg *rest.Config, apiGroups sets.Set[string]) (*ClusterResourceList, error) {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(kc))

	clusterID, err := du.ClusterUID(client)
	if err != nil {
		return nil, err
	}

	gks := make(map[schema.GroupKind]string)
	for _, gvk := range api.RegisteredTypes() {
		if !isResourceAvailable(mapper, gvk) {
			continue
		}
		gk := gvk.GroupKind()
		if v, exists := gks[gk]; !exists || apiversion.MustCompare(v, gvk.Version) < 0 {
			gks[gk] = gvk.Version
		}
	}

	now := time.Now()
	var items []ResourceItem
	for gk, v := range gks {
		if apiGroups.Len() > 0 && !apiGroups.Has(gk.Group) {
			continue
		}
		mapping, err := mapper.RESTMapping(gk, v)
		if meta.IsNoMatchError(err) {
			continue
		} else if err != nil {
			return nil, err
		}

		var ri dynamic.ResourceInterface
		if mapping.Scope == meta.RESTScopeNamespace {
			ri = client.Resource(mapping.Resource).Namespace(core.NamespaceAll)
		} else {
			ri = client.Resource(mapping.Resource)
		}
		result, err := ri.List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, item := range result.Items {
			rr, err := resourcemetrics.AppResourceLimits(item.UnstructuredContent())
			if err != nil {
				return nil, err
			}
			var mem resource.Quantity
			if q, ok := rr[core.ResourceMemory]; ok {
				mem = q
			}
			items = append(items, ResourceItem{
				APIVersion:  mapping.GroupVersionKind.GroupVersion().String(),
				Group:       gk.Group,
				Kind:        gk.Kind,
				Namespace:   item.GetNamespace(),
				Name:        item.GetName(),
				UID:         item.GetUID(),
				Age:         age(item, now),
				MemoryLimit: mem,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})

	return &ClusterResourceList{
		ClusterID: clusterID,
		Context:   ctxName,
		Items:     items,
	}, nil
}

func age(item unstructured.Unstructured, now time.Time) string {
	ts := item.GetCreationTimestamp()
	if ts.IsZero() {
		return "<unknown>"
	}
	return duration.HumanDuration(now.Sub(ts.Time))
}

func printListTable(clusters []*ClusterResourceList) {
	const padding = 3
	for _, c := range clusters {
		_, _ = fmt.Fprintln(os.Stdout, "")
		_, _ = fmt.Fprintf(os.Stdout, "CLUSTER ID: %s\n", c.ClusterID)
		_, _ = fmt.Fprintf(os.Stdout, "KUBECONFIG CONTEXT: %s\n", c.Context)
		_, _ = fmt.Fprintln(os.Stdout, "")

		w := tabwriter.NewWriter(os.Stdout, 0, 0, padding, ' ', tabwriter.TabIndent)
		_, _ = fmt.Fprintln(w, "GROUP/KIND\tNAMESPACE\tNAME\tUID\tAGE\tMEMORY")
		for _, it := range c.Items {
			gk := it.Kind
			if it.Group != "" {
				gk = it.Group + "/" + it.Kind
			}
			ns := it.Namespace
			if ns == "" {
				ns = "-"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", gk, ns, it.Name, it.UID, it.Age, it.MemoryLimit.String())
		}
		_ = w.Flush()
	}
}
