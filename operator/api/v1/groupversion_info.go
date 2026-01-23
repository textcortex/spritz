// +kubebuilder:object:generate=true
// +groupName=spritz.sh
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion = schema.GroupVersion{Group: "spritz.sh", Version: "v1"}

	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)
