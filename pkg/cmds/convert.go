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

	"github.com/spf13/cobra"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	core_util "kmodules.xyz/client-go/core/v1"
	"kubedb.dev/apimachinery/apis/kubedb"
	kubedbv1alpha1 "kubedb.dev/apimachinery/apis/kubedb/v1alpha1"
	cs "kubedb.dev/apimachinery/client/clientset/versioned"
	"sigs.k8s.io/yaml"
)

var registeredKubeDBTypes = []schema.GroupKind{
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindElasticsearch).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindEtcd).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMariaDB).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMemcached).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMongoDB).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindMySQL).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindPerconaXtraDB).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindPostgres).GroupKind(),
	kubedbv1alpha1.SchemeGroupVersion.WithKind(kubedbv1alpha1.ResourceKindRedis).GroupKind(),
}

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
	kc, err := kubernetes.NewForConfig(cfg)
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

	catalogmap, err := LoadCatalog(kubedbclient, false)
	if err != nil {
		return err
	}

	rsmap := map[schema.GroupKind][]interface{}{}
	for _, gk := range registeredKubeDBTypes {
		mapping, err := mapper.RESTMapping(gk)
		if meta.IsNoMatchError(err) {
			continue
		} else if err != nil {
			return err
		}

		ri := dc.Resource(mapping.Resource).Namespace(core.NamespaceAll)
		if result, err := ri.List(context.TODO(), metav1.ListOptions{}); err != nil {
			return err
		} else {
			objects := make([]interface{}, 0, len(result.Items))
			for _, item := range result.Items {
				if gk.Group == kubedb.GroupName && mapping.GroupVersionKind.Version == kubedbv1alpha1.SchemeGroupVersion.Version {
					content, err := Convert_kubedb_v1alpha1_To_v1alpha2(item, catalogmap, topology)
					if err != nil {
						return err
					}
					objects = append(objects, content)
				} else {
					item.SetManagedFields(nil)
					item.SetCreationTimestamp(metav1.Time{})
					item.SetGeneration(0)
					item.SetUID("")
					item.SetResourceVersion("")
					objects = append(objects, item.UnstructuredContent())
				}
			}
			rsmap[gk] = objects
		}
	}

	var buf bytes.Buffer
	for gk, objects := range rsmap {
		for _, obj := range objects {
			buf.Reset()

			name, _, err := unstructured.NestedString(obj.(map[string]interface{}), "metadata", "name")
			if err != nil {
				return err
			}
			namespace, _, err := unstructured.NestedString(obj.(map[string]interface{}), "metadata", "namespace")
			if err != nil {
				return err
			}

			// config secret handling
			if cfgName, ok, _ := unstructured.NestedString(obj.(map[string]interface{}), "spec", "configSecret", "name"); ok && strings.HasPrefix(cfgName, "FIX_CONVERT_TO_SECRET_") {
				cmName := strings.TrimPrefix(cfgName, "FIX_CONVERT_TO_SECRET_")
				if cm, err := kc.CoreV1().ConfigMaps(namespace).Get(context.TODO(), cmName, metav1.GetOptions{}); err == nil {
					s := &core.Secret{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Secret",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:            cm.Name,
							Namespace:       cm.Namespace,
							Labels:          cm.Labels,
							Annotations:     cm.Annotations,
							OwnerReferences: cm.OwnerReferences,
							Finalizers:      cm.Finalizers,
						},
						StringData: cm.Data,
					}
					if data, err := yaml.Marshal(s); err != nil {
						return err
					} else {
						buf.Write(data)
						buf.WriteString("\n---\n")

						_ = unstructured.SetNestedField(obj.(map[string]interface{}), cmName, "spec", "configSecret", "name")
					}
				}
			}

			if data, err := yaml.Marshal(obj); err != nil {
				return err
			} else {
				buf.Write(data)
			}

			err = os.MkdirAll(filepath.Join(dir, strings.ToLower(gk.Kind), namespace, name), 0o755)
			if err != nil {
				return err
			}
			if err := ioutil.WriteFile(filepath.Join(dir, strings.ToLower(gk.Kind), namespace, name, name+".yaml"), buf.Bytes(), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
