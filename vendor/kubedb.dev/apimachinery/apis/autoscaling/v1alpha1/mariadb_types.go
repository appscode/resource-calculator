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
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ResourceCodeMariaDBAutoscaler     = "mdscaler"
	ResourceKindMariaDBAutoscaler     = "MariaDBAutoscaler"
	ResourceSingularMariaDBAutoscaler = "mariadbautoscaler"
	ResourcePluralMariaDBAutoscaler   = "mariadbautoscalers"
)

// MariaDBAutoscaler is the configuration for a mariadb database
// autoscaler, which automatically manages pod resources based on historical and
// real time resource utilization.

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=mariadbautoscalers,singular=mariadbautoscaler,shortName=mdscaler,categories={datastore,kubedb,appscode}
// +kubebuilder:subresource:status
type MariaDBAutoscaler struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object metadata. More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the behavior of the autoscaler.
	// More info: https://git.k8s.io/community/contributors/devel/api-conventions.md#spec-and-status.
	Spec MariaDBAutoscalerSpec `json:"spec"`

	// Current information about the autoscaler.
	// +optional
	Status AutoscalerStatus `json:"status,omitempty"`
}

// MariaDBAutoscalerSpec is the specification of the behavior of the autoscaler.
type MariaDBAutoscalerSpec struct {
	DatabaseRef *core.LocalObjectReference `json:"databaseRef"`

	Compute *MariaDBComputeAutoscalerSpec `json:"compute,omitempty"`
	Storage *MariaDBStorageAutoscalerSpec `json:"storage,omitempty"`
}

type MariaDBComputeAutoscalerSpec struct {
	MariaDB *ComputeAutoscalerSpec `json:"mariadb,omitempty"`
}

type MariaDBStorageAutoscalerSpec struct {
	MariaDB *StorageAutoscalerSpec `json:"mariadb,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// MariaDBAutoscalerList is a list of MariaDBAutoscaler objects.
type MariaDBAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata"`

	// items is the list of mariadb database autoscaler objects.
	Items []MariaDBAutoscaler `json:"items"`
}
