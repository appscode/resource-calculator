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
	"strings"
	"text/tabwriter"

	core_util "kmodules.xyz/client-go/core/v1"
	"kmodules.xyz/client-go/tools/parser"
	resourcemetrics "kmodules.xyz/resource-metrics"
	"kmodules.xyz/resource-metrics/api"

	"github.com/spf13/cobra"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	catalogv1alpha1 "kubedb.dev/apimachinery/apis/catalog/v1alpha1"
	kubedbv1alpha1 "kubedb.dev/apimachinery/apis/kubedb/v1alpha1"
	kubedbv1alpha2 "kubedb.dev/apimachinery/apis/kubedb/v1alpha2"
	"kubedb.dev/installer/catalog"
)

func NewCmdCalculate(clientGetter genericclioptions.RESTClientGetter) *cobra.Command {
	var apiGroups []string
	cmd := &cobra.Command{
		Use:                   "calculate",
		Short:                 "Calculate metrics of a specific group of resources",
		DisableFlagsInUseLine: true,
		DisableAutoGenTag:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(clientGetter, sets.NewString(apiGroups...))
		},
	}
	cmd.Flags().StringSliceVar(&apiGroups, "apiGroups", apiGroups, "api groups for which to calculate resource")

	return cmd
}

type KindVersion struct {
	Kind    string
	Version string
}

func run(clientGetter genericclioptions.RESTClientGetter, apiGroups sets.String) error {
	cfg, err := clientGetter.ToRESTConfig()
	if err != nil {
		return err
	}
	client, err := dynamic.NewForConfig(cfg)
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

	catalogversions, err := parser.ListFSResources(catalog.FS())
	if err != nil {
		return err
	}
	catalogmap := map[KindVersion]interface{}{}
	for _, r := range catalogversions {
		key := r.GetObjectKind().GroupVersionKind()
		key.Kind = strings.TrimSuffix(key.Kind, "Version")

		switch key.Kind {
		case kubedbv1alpha1.ResourceKindElasticsearch:
			var in catalogv1alpha1.ElasticsearchVersion
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(r.UnstructuredContent(), &in); err != nil {
				return err
			}
			catalogmap[KindVersion{
				Kind:    key.Kind,
				Version: r.GetName(),
			}] = &in

		case kubedbv1alpha1.ResourceKindMongoDB:
			var in catalogv1alpha1.MongoDBVersion
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(r.UnstructuredContent(), &in); err != nil {
				return err
			}
			catalogmap[KindVersion{
				Kind:    key.Kind,
				Version: r.GetName(),
			}] = &in

		case kubedbv1alpha1.ResourceKindPostgres:
			var in catalogv1alpha1.PostgresVersion
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(r.UnstructuredContent(), &in); err != nil {
				return err
			}
			catalogmap[KindVersion{
				Kind:    key.Kind,
				Version: r.GetName(),
			}] = &in

		}
	}

	rsmap := map[schema.GroupVersionKind]core.ResourceList{}
	var rrTotal core.ResourceList
	for _, gvk := range api.RegisteredTypes() {
		if apiGroups.Len() > 0 && !apiGroups.Has(gvk.Group) {
			continue
		}

		var mapping *meta.RESTMapping
		if gvk.Group == "kubedb.com" {
			mapping, err = mapper.RESTMapping(gvk.GroupKind())
			if meta.IsNoMatchError(err) {
				rsmap[gvk] = nil // keep track
				continue
			} else if err != nil {
				return err
			}
			gvk = mapping.GroupVersionKind // v1alpha1 or v1alpha2
		} else {
			mapping, err = mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
			if meta.IsNoMatchError(err) {
				rsmap[gvk] = nil // keep track
				continue
			} else if err != nil {
				return err
			}
		}

		var ri dynamic.ResourceInterface
		if mapping.Scope == meta.RESTScopeNamespace {
			ri = client.Resource(mapping.Resource).Namespace(core.NamespaceAll)
		} else {
			ri = client.Resource(mapping.Resource)
		}
		if result, err := ri.List(context.TODO(), metav1.ListOptions{}); err != nil {
			return err
		} else {
			var summary core.ResourceList
			for _, item := range result.Items {
				content := item.UnstructuredContent()

				if gvk.Group == "kubedb.com" && gvk.Version == "v1alpha1" {
					switch gvk.Kind {
					case kubedbv1alpha1.ResourceKindElasticsearch:
						var in kubedbv1alpha1.Elasticsearch
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.Elasticsearch
						if err := kubedbv1alpha1.Convert_v1alpha1_Elasticsearch_To_v1alpha2_Elasticsearch(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						if cv, ok := catalogmap[KindVersion{
							Kind:    gvk.Kind,
							Version: out.Spec.Version,
						}]; ok {
							out.SetDefaults(cv.(*catalogv1alpha1.ElasticsearchVersion), topology)
						} else {
							return fmt.Errorf("unknown %v version %s", gvk, out.Spec.Version)
						}

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}
					case kubedbv1alpha1.ResourceKindEtcd:
						var in kubedbv1alpha1.Etcd
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.Etcd
						if err := kubedbv1alpha1.Convert_v1alpha1_Etcd_To_v1alpha2_Etcd(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults()

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindMariaDB:
						var in kubedbv1alpha1.MariaDB
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.MariaDB
						if err := kubedbv1alpha1.Convert_v1alpha1_MariaDB_To_v1alpha2_MariaDB(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults(topology)

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindMemcached:
						var in kubedbv1alpha1.Memcached
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.Memcached
						if err := kubedbv1alpha1.Convert_v1alpha1_Memcached_To_v1alpha2_Memcached(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults()

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindMongoDB:
						var in kubedbv1alpha1.MongoDB
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.MongoDB
						if err := kubedbv1alpha1.Convert_v1alpha1_MongoDB_To_v1alpha2_MongoDB(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						if cv, ok := catalogmap[KindVersion{
							Kind:    gvk.Kind,
							Version: out.Spec.Version,
						}]; ok {
							out.SetDefaults(cv.(*catalogv1alpha1.MongoDBVersion), topology)
						} else {
							return fmt.Errorf("unknown %v version %s", gvk, out.Spec.Version)
						}

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindMySQL:
						var in kubedbv1alpha1.MySQL
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.MySQL
						if err := kubedbv1alpha1.Convert_v1alpha1_MySQL_To_v1alpha2_MySQL(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults(topology)

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindPerconaXtraDB:
						var in kubedbv1alpha1.PerconaXtraDB
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.PerconaXtraDB
						if err := kubedbv1alpha1.Convert_v1alpha1_PerconaXtraDB_To_v1alpha2_PerconaXtraDB(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults()

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindPostgres:
						var in kubedbv1alpha1.Postgres
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.Postgres
						if err := kubedbv1alpha1.Convert_v1alpha1_Postgres_To_v1alpha2_Postgres(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						if cv, ok := catalogmap[KindVersion{
							Kind:    gvk.Kind,
							Version: out.Spec.Version,
						}]; ok {
							out.SetDefaults(cv.(*catalogv1alpha1.PostgresVersion), topology)
						} else {
							return fmt.Errorf("unknown %v version %s", gvk, out.Spec.Version)
						}

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}

					case kubedbv1alpha1.ResourceKindRedis:
						var in kubedbv1alpha1.Redis
						if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.UnstructuredContent(), &in); err != nil {
							return err
						}
						var out kubedbv1alpha2.Redis
						if err := kubedbv1alpha1.Convert_v1alpha1_Redis_To_v1alpha2_Redis(&in, &out, nil); err != nil {
							return err
						}
						out.APIVersion = kubedbv1alpha2.SchemeGroupVersion.String()
						out.Kind = in.Kind
						out.SetDefaults(topology)

						content, err = runtime.DefaultUnstructuredConverter.ToUnstructured(&out)
						if err != nil {
							return err
						}
					}
				}

				rr, err := resourcemetrics.AppResourceLimits(content)
				if err != nil {
					return err
				}
				summary = api.AddResourceList(summary, rr)
			}
			rsmap[gvk] = summary
			rrTotal = api.AddResourceList(rrTotal, summary)
		}
	}

	gvks := make([]schema.GroupVersionKind, 0, len(rsmap))
	for gvk := range rsmap {
		gvks = append(gvks, gvk)
	}
	sort.Slice(gvks, func(i, j int) bool {
		if gvks[i].Group == gvks[j].Group {
			return gvks[i].Kind < gvks[j].Kind
		}
		return gvks[i].Group < gvks[j].Group
	})

	const padding = 3
	w := tabwriter.NewWriter(os.Stdout, 0, 0, padding, ' ', tabwriter.TabIndent)
	_, _ = fmt.Fprintln(w, "API VERSION\tKIND\tCPU\tMEMORY\tSTORAGE\t")
	for _, gvk := range gvks {
		rr := rsmap[gvk]
		if rr == nil {
			_, _ = fmt.Fprintf(w, "%s\t%s\t-\t-\t-\t\n", gvk.GroupVersion(), gvk.Kind)
		} else {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t\n", gvk.GroupVersion(), gvk.Kind, rr.Cpu(), rr.Memory(), rr.Storage())
		}
	}
	_, _ = fmt.Fprintf(w, "TOTAL\t=\t%s\t%s\t%s\t\n", rrTotal.Cpu(), rrTotal.Memory(), rrTotal.Storage())
	return w.Flush()
}
