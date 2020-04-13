/*
Copyright 2019 The Kubernetes Authors.

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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ClusterFinalizer allows ReconcileAzureStackHCICluster to clean up AzureStackHCI resources associated with AzureStackHCICluster before
	// removing it from the apiserver.
	ClusterFinalizer = "azurestackhcicluster.infrastructure.cluster.x-k8s.io"
)

// AzureStackHCIClusterSpec defines the desired state of AzureStackHCICluster
type AzureStackHCIClusterSpec struct {
	// NetworkSpec encapsulates all things related to AzureStackHCI network.
	NetworkSpec NetworkSpec `json:"networkSpec,omitempty"`

	ResourceGroup string `json:"resourceGroup"`

	Location string `json:"location"`

	// LoadBalancerRef may be used to enable a control plane load balancer for this cluster.
	LoadBalancerRef *corev1.ObjectReference `json:"loadBalancerRef,omitempty"`
}

// AzureStackHCIClusterStatus defines the observed state of AzureStackHCICluster
type AzureStackHCIClusterStatus struct {
	Network Network `json:"network,omitempty"`

	Bastion VM `json:"bastion,omitempty"`

	// Ready is true when the provider resource is ready.
	// +optional
	Ready bool `json:"ready"`

	// APIEndpoints represents the endpoints to communicate with the control plane.
	// +optional
	APIEndpoints []APIEndpoint `json:"apiEndpoints,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=azurestackhciclusters,scope=Namespaced,categories=cluster-api
// +kubebuilder:storageversion
// +kubebuilder:subresource:status

// AzureStackHCICluster is the Schema for the azurestackhciclusters API
type AzureStackHCICluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AzureStackHCIClusterSpec   `json:"spec,omitempty"`
	Status AzureStackHCIClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AzureStackHCIClusterList contains a list of AzureStackHCICluster
type AzureStackHCIClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AzureStackHCICluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AzureStackHCICluster{}, &AzureStackHCIClusterList{})
}