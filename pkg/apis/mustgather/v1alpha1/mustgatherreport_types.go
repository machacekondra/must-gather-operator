package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MustGatherReportSpec defines the desired state of MustGatherReport
// +k8s:openapi-gen=true
type MustGatherReportSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Images      []string      `json:"images"`
	DeleteAfter time.Duration `json:"deleteAfter,omitempty"`
}

// MustGatherReportStatus defines the observed state of MustGatherReport
// +k8s:openapi-gen=true
type MustGatherReportStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	ReportURL string `json:"reportUrl,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MustGatherReport is the Schema for the mustgatherreports API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type MustGatherReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MustGatherReportSpec   `json:"spec,omitempty"`
	Status MustGatherReportStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MustGatherReportList contains a list of MustGatherReport
type MustGatherReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MustGatherReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MustGatherReport{}, &MustGatherReportList{})
}
