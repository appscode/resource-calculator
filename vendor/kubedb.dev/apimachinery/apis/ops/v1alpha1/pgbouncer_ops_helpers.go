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

func (_ PgBouncerOpsRequest) CustomResourceDefinition() *apiextensions.CustomResourceDefinition {
	return crds.MustCustomResourceDefinition(SchemeGroupVersion.WithResource(ResourcePluralPgBouncerOpsRequest))
}

var _ apis.ResourceInfo = &PgBouncerOpsRequest{}

func (p PgBouncerOpsRequest) ResourceFQN() string {
	return fmt.Sprintf("%s.%s", ResourcePluralPgBouncerOpsRequest, ops.GroupName)
}

func (p PgBouncerOpsRequest) ResourceShortCode() string {
	return ResourceCodePgBouncerOpsRequest
}

func (p PgBouncerOpsRequest) ResourceKind() string {
	return ResourceKindPgBouncerOpsRequest
}

func (p PgBouncerOpsRequest) ResourceSingular() string {
	return ResourceSingularPgBouncerOpsRequest
}

func (p PgBouncerOpsRequest) ResourcePlural() string {
	return ResourcePluralPgBouncerOpsRequest
}

func (p PgBouncerOpsRequest) ValidateSpecs() error {
	return nil
}

var _ Accessor = &PgBouncerOpsRequest{}

func (e *PgBouncerOpsRequest) GetObjectMeta() metav1.ObjectMeta {
	return e.ObjectMeta
}

func (e *PgBouncerOpsRequest) GetRequestType() OpsRequestType {
	return e.Spec.Type
}

func (e *PgBouncerOpsRequest) GetDBRefName() string {
	return e.Spec.DatabaseRef.Name
}

func (e *PgBouncerOpsRequest) GetStatus() OpsRequestStatus {
	return e.Status
}

func (e *PgBouncerOpsRequest) SetStatus(s OpsRequestStatus) {
	e.Status = s
}
