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

package v1alpha1

import (
	"fmt"

	"kubedb.dev/apimachinery/apis"
	"kubedb.dev/apimachinery/apis/ops"
	"kubedb.dev/apimachinery/crds"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kmodules.xyz/client-go/apiextensions"
)

func (_ RedisSentinelOpsRequest) CustomResourceDefinition() *apiextensions.CustomResourceDefinition {
	return crds.MustCustomResourceDefinition(SchemeGroupVersion.WithResource(ResourcePluralRedisSentinelOpsRequest))
}

var _ apis.ResourceInfo = &RedisSentinelOpsRequest{}

func (r RedisSentinelOpsRequest) ResourceFQN() string {
	return fmt.Sprintf("%s.%s", ResourcePluralRedisSentinelOpsRequest, ops.GroupName)
}

func (r RedisSentinelOpsRequest) ResourceShortCode() string {
	return ResourceCodeRedisSentinelOpsRequest
}

func (r RedisSentinelOpsRequest) ResourceKind() string {
	return ResourceKindRedisSentinelOpsRequest
}

func (r RedisSentinelOpsRequest) ResourceSingular() string {
	return ResourceSingularRedisSentinelOpsRequest
}

func (r RedisSentinelOpsRequest) ResourcePlural() string {
	return ResourcePluralRedisSentinelOpsRequest
}

func (r RedisSentinelOpsRequest) ValidateSpecs() error {
	return nil
}

var _ Accessor = &RedisSentinelOpsRequest{}

func (e *RedisSentinelOpsRequest) GetObjectMeta() metav1.ObjectMeta {
	return e.ObjectMeta
}

func (e *RedisSentinelOpsRequest) GetRequestType() OpsRequestType {
	return e.Spec.Type
}

func (e *RedisSentinelOpsRequest) GetDBRefName() string {
	return e.Spec.DatabaseRef.Name
}

func (e *RedisSentinelOpsRequest) GetStatus() OpsRequestStatus {
	return e.Status
}

func (e *RedisSentinelOpsRequest) SetStatus(s OpsRequestStatus) {
	e.Status = s
}
