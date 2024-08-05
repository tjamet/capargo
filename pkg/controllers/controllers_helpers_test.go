package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/secret"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testAnnotationPrefix = "example.com"

	defaultTestArgoCDNamespace  = "test-argocd"
	defaultTestClusterName      = "cluster-1"
	defaultTestClusterNamespace = "default"
)

type argoCDSecretBuilder struct {
	*v1.Secret
}

func NewArgoCDClusterSecretBuilder() argoCDSecretBuilder {
	return argoCDSecretBuilder{Secret: &v1.Secret{}}.
		WithArgoCDSecretType(ArgoCDSecretTypeCluster).
		WithClusterObjectRef(defaultTestClusterNamespace, defaultTestClusterName).
		WithNamespace(defaultTestArgoCDNamespace)
}

func (b argoCDSecretBuilder) Build() *v1.Secret {
	return b.Secret
}

func (b argoCDSecretBuilder) WithNamespace(namespace string) argoCDSecretBuilder {
	b.ObjectMeta.Namespace = namespace
	return b
}

func (b argoCDSecretBuilder) WithLabels(labels map[string]string) argoCDSecretBuilder {
	b.Labels = labels
	return b
}

func (b argoCDSecretBuilder) WithData(data map[string][]byte) argoCDSecretBuilder {
	b.Data = data
	return b
}

func (b argoCDSecretBuilder) WithName(name string) argoCDSecretBuilder {
	b.ObjectMeta.Name = name
	return b
}

func (b argoCDSecretBuilder) WithArgoCDSecretType(kind string) argoCDSecretBuilder {
	if b.ObjectMeta.Labels == nil {
		b.ObjectMeta.Labels = map[string]string{}
	}
	b.ObjectMeta.Labels["argocd.argoproj.io/secret-type"] = kind
	return b
}

func (b argoCDSecretBuilder) WithClusterObjectRef(namespace, name string) argoCDSecretBuilder {
	b.WithName(argoCDSecretName(name))
	if b.ObjectMeta.Annotations == nil {
		b.ObjectMeta.Annotations = map[string]string{}
	}
	b.ObjectMeta.Annotations[testAnnotationPrefix+"/cluster-object-namespace"] = namespace
	b.ObjectMeta.Annotations[testAnnotationPrefix+"/cluster-object-name"] = name
	return b
}

type capiSecretBuilder struct {
	cluster       *clusterv1.Cluster
	clusterConfig api.Cluster
	authInfo      api.AuthInfo
	purpose       secret.Purpose
}

func NewCAPIArgoCDSecretBuilder() *capiSecretBuilder {
	return (&capiSecretBuilder{}).
		WithCluster(NewClusterBuilder().Build()).
		WithURL("https://cluster.example.com").
		WithPurpose(secret.Kubeconfig)
}

func (b *capiSecretBuilder) Build(t *testing.T) *v1.Secret {
	t.Helper()
	clusterName := defaultTestClusterName
	if b.cluster != nil {
		clusterName = b.cluster.Name
	}
	cfg := api.Config{
		Clusters: map[string]*api.Cluster{
			clusterName: &b.clusterConfig,
		},
		AuthInfos: map[string]*api.AuthInfo{
			clusterName: &b.authInfo,
		},
		Contexts: map[string]*api.Context{
			clusterName: {
				Cluster:  clusterName,
				AuthInfo: clusterName,
			},
		},
		CurrentContext: clusterName,
	}
	out, err := clientcmd.Write(cfg)
	require.NoError(t, err)

	secretName := secret.Name(clusterName, b.purpose)
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: defaultTestArgoCDNamespace,
		},
		Data: map[string][]byte{
			secret.KubeconfigDataName: out,
		},
	}
	if b.cluster != nil {
		secret = kubeconfig.GenerateSecret(b.cluster, out)
	}
	secret.Name = secretName
	return secret
}

func (b *capiSecretBuilder) WithCluster(cluster *clusterv1.Cluster) *capiSecretBuilder {
	b.cluster = cluster
	return b
}

func (b *capiSecretBuilder) WithPurpose(purpose secret.Purpose) *capiSecretBuilder {
	b.purpose = purpose
	return b
}

func (b *capiSecretBuilder) WithURL(url string) *capiSecretBuilder {
	b.clusterConfig.Server = url
	return b
}

func (b *capiSecretBuilder) WithInsecure(insecure bool) *capiSecretBuilder {
	b.clusterConfig.InsecureSkipTLSVerify = insecure
	return b
}

func (b *capiSecretBuilder) WithCACertData(caCertData []byte) *capiSecretBuilder {
	b.clusterConfig.CertificateAuthorityData = caCertData
	return b
}

func (b *capiSecretBuilder) WithBearer(bearer string) *capiSecretBuilder {
	b.authInfo.Token = bearer
	return b
}

func (b *capiSecretBuilder) WithClientCertificatData(certData []byte) *capiSecretBuilder {
	b.authInfo.ClientCertificateData = certData
	return b
}

func (b *capiSecretBuilder) WithClientKeyData(keyData []byte) *capiSecretBuilder {
	b.authInfo.ClientKeyData = keyData
	return b
}

type clusterBuilder struct {
	*clusterv1.Cluster
}

func NewClusterBuilder() *clusterBuilder {
	return (&clusterBuilder{Cluster: &clusterv1.Cluster{}}).
		WithName(defaultTestClusterName).
		WithNamespace(defaultTestClusterNamespace)
}

func (b *clusterBuilder) WithName(name string) *clusterBuilder {
	b.Cluster.Name = name
	return b
}

func (b *clusterBuilder) WithNamespace(namespace string) *clusterBuilder {
	b.Cluster.Namespace = namespace
	return b
}

func (b *clusterBuilder) WithLabels(labels map[string]string) *clusterBuilder {
	b.Cluster.Labels = labels
	return b
}

func (b *clusterBuilder) Build() *clusterv1.Cluster {
	return b.Cluster
}

func assertClientHasObjects(t *testing.T, k8sClient client.Client, objs ...client.Object) {
	t.Helper()
	for _, obj := range objs {
		key := client.ObjectKeyFromObject(obj)
		assert.NoError(t, k8sClient.Get(context.Background(), key, obj))
	}
}

func assertClientHasNoObjects(t *testing.T, k8sClient client.Client, objs ...client.Object) {
	t.Helper()
	for _, obj := range objs {
		key := client.ObjectKeyFromObject(obj)
		err := k8sClient.Get(context.Background(), key, obj)
		assert.True(t, apierrors.IsNotFound(err))
	}
}
