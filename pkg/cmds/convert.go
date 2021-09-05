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
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	core_util "kmodules.xyz/client-go/core/v1"

	"github.com/spf13/cobra"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	"kubedb.dev/apimachinery/apis/kubedb"
	kubedbv1alpha1 "kubedb.dev/apimachinery/apis/kubedb/v1alpha1"
	"sigs.k8s.io/yaml"
)

func NewCmdConvert(clientGetter genericclioptions.RESTClientGetter) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:                   "convert",
		Short:                 "Convert KubeDB resources from v1alpha1 to v1alpha2 api",
		DisableFlagsInUseLine: true,
		DisableAutoGenTag:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return convert(dir, clientGetter)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", dir, "Path to directory where yaml files are written")

	return cmd
}

func convert(dir string, clientGetter genericclioptions.RESTClientGetter) error {
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

	catalogmap, err := parseCatalog()
	if err != nil {
		return err
	}

	registeredTypes := []schema.GroupVersionKind{
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindElasticsearch),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindEtcd),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMariaDB),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMemcached),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMongoDB),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMySQL),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindPerconaXtraDB),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindPostgres),
		kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindRedis),
	}

	rsmap := map[schema.GroupVersionKind][]interface{}{}
	for _, gvk := range registeredTypes {
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if meta.IsNoMatchError(err) {
			continue
		} else if err != nil {
			return err
		}

		ri := client.Resource(mapping.Resource).Namespace(core.NamespaceAll)
		if result, err := ri.List(context.TODO(), metav1.ListOptions{}); err != nil {
			return err
		} else {
			objects := make([]interface{}, 0, len(result.Items))
			for _, item := range result.Items {
				if gvk.Group == kubedb.GroupName && gvk.Version == "v1alpha1" {
					content, err := Convert_kubedb_v1alpha1_To_v1alpha2(item, catalogmap, topology)
					if err != nil {
						return err
					}
					objects = append(objects, content)
				} else {
					objects = append(objects, item.UnstructuredContent())
				}
			}
			rsmap[gvk] = objects
		}
	}

	var buf bytes.Buffer
	for gvk, objects := range rsmap {
		for _, obj := range objects {
			buf.Reset()

			if data, err := yaml.Marshal(obj); err != nil {
				return err
			} else {
				buf.Write(data)
			}
			name, _, err := unstructured.NestedString(obj.(map[string]interface{}), "metadata", "name")
			if err != nil {
				return err
			}
			namespace, _, err := unstructured.NestedString(obj.(map[string]interface{}), "metadata", "namespace")
			if err != nil {
				return err
			}
			err = os.MkdirAll(filepath.Join(dir, namespace, strings.ToLower(gvk.Kind)), 0755)
			if err != nil {
				return err
			}
			if err := ioutil.WriteFile(filepath.Join(dir, namespace, strings.ToLower(gvk.Kind), name+".yaml"), buf.Bytes(), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}
