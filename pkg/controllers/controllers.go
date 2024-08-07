package controllers

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	AnnotationPrefix = "capargo.jamet.dev"

	CapargoArgoCD = "argocd"

	ClusterNameAnnotationSuffix      = "cluster-object-name"
	ClusterNamespaceAnnotationSuffix = "cluster-object-namespace"

	ArgoCDSecretTypeLabelName = "argocd.argoproj.io/secret-type"

	ArgoCDSecretTypeCluster = "cluster"

	ArgoCDCompareAnnotation     = "argocd.argoproj.io/compare-options"
	ArgoCDSyncOptionsAnnotation = "argocd.argoproj.io/sync-options"

	ArgoCDIgnoreExtraneous = "IgnoreExtraneous"
)

type ArgoCDCompareOptions []ArgoCDSyncOption

func (o ArgoCDCompareOptions) AnnotationValue() string {
	var result string
	for i, option := range o {
		if i > 0 {
			result += ","
		}
		result += option.Kind + "=" + option.Value
	}
	return result
}

type ArgoCDSyncOption struct {
	Kind  string
	Value string
}

type CapargoMode string

var (
	CapargoEnable  CapargoMode = "enable"
	CapargoDisable CapargoMode = "disable"
)

func isEnabled(mode string) bool {
	return mode == string(CapargoEnable) || mode == "enabled"
}

func annotation(prefix, suffix string) string {
	return prefix + "/" + suffix
}

func argoCDSecretName(cluster string) string {
	return "capargo-" + cluster
}

func Scheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	err := v1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}
	err = capiv1beta1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}

	err = clientgoscheme.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}
	return scheme
}
