/*
Copyright 2021.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MachineDeletionRemediationTemplateResource is part of the desired state of MachineDeletionRemediationTemplate
type MachineDeletionRemediationTemplateResource struct {
	Spec MachineDeletionRemediationSpec `json:"spec"`
}

// MachineDeletionRemediationTemplateSpec defines the desired state of MachineDeletionRemediationTemplate
type MachineDeletionRemediationTemplateSpec struct {
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Template MachineDeletionRemediationTemplateResource `json:"template"`
}

// MachineDeletionRemediationTemplateStatus defines the observed state of MachineDeletionRemediationTemplate
type MachineDeletionRemediationTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MachineDeletionRemediationTemplate is the Schema for the machinedeletionremediationtemplates API
// +operator-sdk:csv:customresourcedefinitions:resources={{"MachineDeletionRemediationTemplate","v1alpha1","machinedeletionremediationtemplates"}}
type MachineDeletionRemediationTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MachineDeletionRemediationTemplateSpec   `json:"spec,omitempty"`
	Status MachineDeletionRemediationTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MachineDeletionRemediationTemplateList contains a list of MachineDeletionRemediationTemplate
type MachineDeletionRemediationTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MachineDeletionRemediationTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MachineDeletionRemediationTemplate{}, &MachineDeletionRemediationTemplateList{})
}
