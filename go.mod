module github.com/openconfig/gribigo

go 1.16

require (
	github.com/golang/glog v1.0.0
	github.com/google/go-cmp v0.5.9
	github.com/google/protobuf v3.14.0+incompatible // indirect
	github.com/google/uuid v1.3.0
	github.com/kentik/patricia v1.2.0
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/moby/term v0.0.0-20220808134915-39b0c02b01ae // indirect
	github.com/openconfig/gnmi v0.0.0-20220920173703-480bf53a74d2
	github.com/openconfig/goyang v1.2.0
	github.com/openconfig/gribi v0.1.1-0.20230111180713-7ea0c4e1ee20
	github.com/openconfig/lemming v0.1.1-0.20230307003050-67b3edf1d7da
	github.com/openconfig/testt v0.0.0-20220311054427-efbb1a32ec07
	github.com/openconfig/ygot v0.25.7
	github.com/prometheus/client_golang v1.14.0 // indirect
	github.com/prometheus/common v0.38.0 // indirect
	go.uber.org/atomic v1.9.0
	google.golang.org/genproto v0.0.0-20230216225411-c8e22ba71e44
	google.golang.org/grpc v1.53.0
	google.golang.org/protobuf v1.28.1
	lukechampine.com/uint128 v1.1.1
	sigs.k8s.io/controller-runtime v0.13.1 // indirect
)

// pinning to specific versions is required because lemming indirectly depends
// on metallb via kne, and metallb uses the k8s.io/kubernetes module which is
// not recommended.
// See https://github.com/kubernetes/kubernetes/issues/90358#issuecomment-617859364
replace (
	k8s.io/api => k8s.io/api v0.24.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.24.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.24.0
	k8s.io/apiserver => k8s.io/apiserver v0.24.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.24.0
	k8s.io/client-go => k8s.io/client-go v0.24.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.24.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.24.0
	k8s.io/code-generator => k8s.io/code-generator v0.24.0
	k8s.io/component-base => k8s.io/component-base v0.24.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.24.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.24.0
	k8s.io/cri-api => k8s.io/cri-api v0.24.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.24.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.24.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.24.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.24.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.24.0
	k8s.io/kubectl => k8s.io/kubectl v0.24.0
	k8s.io/kubelet => k8s.io/kubelet v0.24.0
	k8s.io/kubernetes => k8s.io/kubernetes v1.24.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.24.0
	k8s.io/metrics => k8s.io/metrics v0.24.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.24.0
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.24.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.24.0
)
