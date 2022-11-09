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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gomodules.xyz/sets"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"kmodules.xyz/client-go/tools/parser"
	api "kubedb.dev/apimachinery/apis/catalog/v1alpha1"
)

func NewCmdListImages() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:                   "list",
		Short:                 "List all Docker images in a dir/file or stdin",
		DisableFlagsInUseLine: true,
		DisableAutoGenTag:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ListImages(args)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", dir, "Path to directory where yaml files are written")

	return cmd
}

func ListImages(args []string) error {
	if len(args) == 0 {
		return errors.New("missing input")
	} else if len(args) > 1 {
		return errors.New("too many inputs")
	}

	imgList := sets.NewString()
	dir := args[0]
	if dir == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		err = parser.ProcessResources(data, processYAML(imgList))
		if err != nil {
			return err
		}
	} else {
		err := parser.ProcessPath(dir, processYAML(imgList))
		if err != nil {
			return err
		}
	}

	fmt.Println(strings.Join(imgList.List(), "\n"))
	return nil
}

func processYAML(imgList sets.String) func(ri parser.ResourceInfo) error {
	return func(ri parser.ResourceInfo) error {
		switch ri.Object.GetObjectKind().GroupVersionKind() {
		case core.SchemeGroupVersion.WithKind("ReplicationController"):
			var obj core.ReplicationController
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.Template.Spec.InitContainers, imgList)
		case apps.SchemeGroupVersion.WithKind("Deployment"):
			var obj apps.Deployment
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.Template.Spec.InitContainers, imgList)
		case apps.SchemeGroupVersion.WithKind("StatefulSet"):
			var obj apps.StatefulSet
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.Template.Spec.InitContainers, imgList)
		case apps.SchemeGroupVersion.WithKind("DaemonSet"):
			var obj apps.DaemonSet
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.Template.Spec.InitContainers, imgList)
		case batch.SchemeGroupVersion.WithKind("Job"):
			var obj batch.Job
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.Template.Spec.InitContainers, imgList)
		case batch.SchemeGroupVersion.WithKind("CronJob"):
			var obj batch.CronJob
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &obj); err != nil {
				return err
			}
			collectFromContainers(obj.Spec.JobTemplate.Spec.Template.Spec.Containers, imgList)
			collectFromContainers(obj.Spec.JobTemplate.Spec.Template.Spec.InitContainers, imgList)
		case api.SchemeGroupVersion.WithKind(api.ResourceKindElasticsearchVersion):
			var v api.ElasticsearchVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Dashboard.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.DashboardInitContainer.YQImage, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindMemcachedVersion):
			var v api.MemcachedVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindMariaDBVersion):
			var v api.MariaDBVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Coordinator.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindMongoDBVersion):
			var v api.MongoDBVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.ReplicationModeDetector.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindMySQLVersion):
			var v api.MySQLVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.ReplicationModeDetector.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Coordinator.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Router.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.RouterInitContainer.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindPerconaXtraDBVersion):
			var v api.PerconaXtraDBVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindPgBouncerVersion):
			var v api.PgBouncerVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.PgBouncer.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindProxySQLVersion):
			var v api.ProxySQLVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.Proxysql.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindRedisVersion):
			var v api.RedisVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Coordinator.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
		case api.SchemeGroupVersion.WithKind(api.ResourceKindPostgresVersion):
			var v api.PostgresVersion
			err := runtime.DefaultUnstructuredConverter.FromUnstructured(ri.Object.UnstructuredContent(), &v)
			if err != nil {
				panic(err)
			}

			if err := collect(v.Spec.DB.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Coordinator.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.Exporter.Image, imgList); err != nil {
				panic(err)
			}
			if err := collect(v.Spec.InitContainer.Image, imgList); err != nil {
				panic(err)
			}
		}
		return nil
	}
}

func collect(ref string, dm sets.String) error {
	if ref == "" {
		return nil
	}
	dm.Insert(ref)
	return nil
}

func collectFromContainers(containers []core.Container, dm sets.String) {
	for _, c := range containers {
		dm.Insert(c.Image)
	}
}
