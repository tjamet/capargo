package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/secret"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func clusterEnablementTestCase(t *testing.T, enableByDefault bool, clusterLabels map[string]string, check func(t *testing.T, k8sClient client.Client, object ...client.Object)) {
	t.Helper()
	cluster := NewClusterBuilder().WithLabels(clusterLabels).Build()
	clusterSecret := NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t)
	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(cluster, clusterSecret).Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  defaultTestArgoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  enableByDefault,
	}

	result, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	check(t, k8sClient, NewArgoCDClusterSecretBuilder().Build())
}

func TestClusterReconcilerHandlesManagementLabels(t *testing.T) {
	t.Run("When capargo is enabled by default", func(t *testing.T) {
		t.Run("When the cluster has no enablement label", func(t *testing.T) {
			clusterEnablementTestCase(t, true, nil, assertClientHasObjects)
		})
		t.Run("When the cluster has explicit disable label", func(t *testing.T) {
			clusterEnablementTestCase(t, true, map[string]string{annotation(testAnnotationPrefix, CapargoArgoCD): string(CapargoDisable)}, assertClientHasNoObjects)
		})
	})
	t.Run("When capargo is disabled by default", func(t *testing.T) {
		t.Run("When the cluster has no enablement label", func(t *testing.T) {
			clusterEnablementTestCase(t, false, nil, assertClientHasNoObjects)
		})
		t.Run("When the cluster has explicit enable label", func(t *testing.T) {
			clusterEnablementTestCase(t, false, map[string]string{annotation(testAnnotationPrefix, CapargoArgoCD): string(CapargoEnable)}, assertClientHasObjects)
		})
		t.Run("When the cluster has explicit enabled label", func(t *testing.T) {
			clusterEnablementTestCase(t, false, map[string]string{annotation(testAnnotationPrefix, CapargoArgoCD): string(CapargoEnable) + "d"}, assertClientHasObjects)
		})
	})
}

func TestClusterSecretReconcileDeletedSecretsFromNonExistingClusters(t *testing.T) {
	ctx := context.TODO()

	argoCDNamespace := "argocd"
	clusterNamespace, clusterName := "my-namespace", "my-cluster"

	secret := NewArgoCDClusterSecretBuilder().WithClusterObjectRef(clusterNamespace, clusterName).WithNamespace(argoCDNamespace).Build()

	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(secret).Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}})
	require.NoError(t, err)

	assertClientHasNoObjects(t, k8sClient, secret)
}

func TestClusterReconcilerSkipsNonArgoCDClusterObjects(t *testing.T) {

	ctx := context.TODO()

	argoCDNamespace := "argocd"

	secret := NewArgoCDClusterSecretBuilder().WithNamespace(argoCDNamespace).WithArgoCDSecretType("repo").Build()

	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(secret).Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(secret)})
	require.NoError(t, err)

	assertClientHasObjects(t, k8sClient, secret)
}

type queueFunc struct {
	workqueue.RateLimitingInterface
	AddRateLimitedCount int
	AddRateLimitedFunc  func(item interface{})
}

func (q *queueFunc) AddRateLimited(item interface{}) {
	q.AddRateLimitedCount++
	if q.AddRateLimitedFunc != nil {
		q.AddRateLimitedFunc(item)
	}
}

func ClusterReconcilerWithArgoCDClusterObjectsOutsideFromArgoCDNamespaceTestCase(t *testing.T, argoCDNamespace, secretNamespace string, enqueued bool) {

	ctx := context.TODO()

	secret := NewArgoCDClusterSecretBuilder().WithNamespace(secretNamespace).Build()

	handler := clusterSecretEventHandler{
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
	}

	t.Run("When the secret is created", func(t *testing.T) {
		q := queueFunc{}
		handler.Create(ctx, event.CreateEvent{Object: secret}, &q)
		assert.Equal(t, enqueued, q.AddRateLimitedCount > 0)
	})
	t.Run("When the secret is updated", func(t *testing.T) {
		q := queueFunc{}
		handler.Update(ctx, event.UpdateEvent{ObjectNew: secret}, &q)
		assert.Equal(t, enqueued, q.AddRateLimitedCount > 0)
	})
	t.Run("When the secret is deleted", func(t *testing.T) {
		q := queueFunc{}
		handler.Delete(ctx, event.DeleteEvent{Object: secret}, &q)
		assert.Equal(t, enqueued, q.AddRateLimitedCount > 0)
	})

}

func TestClusterReconcilerWithArgoCDClusterObjectssOutsideFromArgoCDNamespace(t *testing.T) {
	t.Run("When the argocd cluster is outside from the argocd namespace", func(t *testing.T) {
		ClusterReconcilerWithArgoCDClusterObjectsOutsideFromArgoCDNamespaceTestCase(t, "test-argocd", "other-namespace", false)
	})
	t.Run("When the argocd cluster is inside from the argocd namespace", func(t *testing.T) {
		// Because the test does not provision the cluster object, the secret will be deleted
		ClusterReconcilerWithArgoCDClusterObjectsOutsideFromArgoCDNamespaceTestCase(t, "test-argocd", "test-argocd", true)
	})
}

func TestReconcileReqFromSecret(t *testing.T) {
	ctx := context.TODO()
	argoCDNamespace := "test-argocd"
	assert.Nil(t, reconcileReqFromSecret(ctx, testAnnotationPrefix, argoCDNamespace, &v1.Secret{}))
	t.Run("When the secret is a valid argoCD secret inside the argoCD namespace", func(t *testing.T) {
		assert.Equal(
			t,
			&reconcile.Request{NamespacedName: types.NamespacedName{Name: "cluster1", Namespace: "default"}},
			reconcileReqFromSecret(ctx, testAnnotationPrefix, argoCDNamespace, NewArgoCDClusterSecretBuilder().WithNamespace(argoCDNamespace).WithClusterObjectRef("default", "cluster1").Build()),
		)
	})
	t.Run("When the secret is a valid capi secret namespace", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "default",
			},
		}
		assert.Equal(
			t,
			&reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cluster)},
			reconcileReqFromSecret(ctx, testAnnotationPrefix, argoCDNamespace, NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t)),
		)
	})
}

func ClusterReconcilerCreatesArgoCDClusterObjectInTheRelevantNamespaceTestCase(t *testing.T, reconcilerNamespace, clusterNamespace, expectedArgoClusterNamespace string) {
	t.Helper()
	ctx := context.TODO()

	clusterName := "my-cluster"
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
	}

	clusterSecret := NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t)
	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(clusterSecret, cluster).
		Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  reconcilerNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	require.NoError(t, err)

	argoSecret := NewArgoCDClusterSecretBuilder().WithNamespace(expectedArgoClusterNamespace).WithName(argoCDSecretName(clusterName)).Build()
	assertClientHasObjects(t, k8sClient, argoSecret)
}

func TestClusterReconcilerCreatesArgoCDClusterObjectInTheRelevantNamespace(t *testing.T) {
	ClusterReconcilerCreatesArgoCDClusterObjectInTheRelevantNamespaceTestCase(t, "test-argocd", "test-argocd", "test-argocd")
	ClusterReconcilerCreatesArgoCDClusterObjectInTheRelevantNamespaceTestCase(t, "", "test-argocd", "test-argocd")
	ClusterReconcilerCreatesArgoCDClusterObjectInTheRelevantNamespaceTestCase(t, "", "capi", "capi")
}

func TestClusterAPIKubeConfigSecretReconcile(t *testing.T) {
	ctx := context.TODO()
	clusterName := "my-cluster"
	argoCDNamespace := "test-argocd"

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				"domain.example.com/kind":    "value",
				"domain.example.com/version": "0",
			},
		},
	}

	clusterSecret := NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t)

	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(clusterSecret, cluster).
		Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	require.NoError(t, err)

	argoSecret := NewArgoCDClusterSecretBuilder().WithNamespace(argoCDNamespace).WithName(argoCDSecretName(clusterName)).Build()

	assertClientHasObjects(t, k8sClient, argoSecret)

	assert.Equal(
		t,
		map[string]string{
			ArgoCDSecretTypeLabelName:    ArgoCDSecretTypeCluster,
			"domain.example.com/kind":    "value",
			"domain.example.com/version": "0",
		},
		argoSecret.Labels,
	)
}

func TestClusterAPIKubeConfigSecretReconcileUpdatesArgocdSecret(t *testing.T) {
	ctx := context.TODO()
	clusterName := "my-cluster"
	argoCDNamespace := "test-argocd"
	server := "https://server.example.com"

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: argoCDNamespace,
			Labels: map[string]string{
				"domain.example.com/version": "10",
			},
		},
	}

	clusterSecret := NewCAPIArgoCDSecretBuilder().WithCluster(cluster).WithURL(server).Build(t)
	argoSecret := NewArgoCDClusterSecretBuilder().
		WithNamespace(argoCDNamespace).
		WithName(argoCDSecretName(clusterName)).
		WithData(map[string][]byte{"name": []byte("old"), "other": []byte("old")}).
		WithLabels(map[string]string{"domain.example.com/version": "0", "domain.example.com/kind": "kind"}).
		Build()

	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(argoSecret, clusterSecret, cluster).
		Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	require.NoError(t, err)

	assertClientHasObjects(t, k8sClient, argoSecret)

	assert.Equal(
		t,
		map[string]string{
			ArgoCDSecretTypeLabelName:    ArgoCDSecretTypeCluster,
			"domain.example.com/version": "10",
		},
		argoSecret.Labels,
	)
	assert.Len(t, argoSecret.Data, 3)
	assert.Equal(t, "my-cluster", string(argoSecret.Data["name"]))
	assert.Equal(t, server, string(argoSecret.Data["server"]))
	assert.Equal(t, "{}", string(argoSecret.Data["config"]))
}

func clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t *testing.T, builder *capiSecretBuilder, expectedJsonConfig string) {
	t.Helper()
	ctx := context.TODO()
	clusterName := "my-cluster"
	serverURL := "https://cluster.example.com"
	argoCDNamespace := "test-argocd"

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: argoCDNamespace,
		},
	}

	clusterSecret := builder.WithCluster(cluster).Build(t)
	k8sClient := fake.NewClientBuilder().
		WithScheme(Scheme()).
		WithObjects(clusterSecret, cluster).
		Build()

	reconciler := &ClusterReconciler{
		Client:           k8sClient,
		ArgoCDNamespace:  argoCDNamespace,
		AnnotationPrefix: testAnnotationPrefix,
		EnableByDefault:  true,
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
	require.NoError(t, err)

	argoSecret := NewArgoCDClusterSecretBuilder().WithNamespace(argoCDNamespace).WithName(argoCDSecretName(clusterName)).Build()

	assertClientHasObjects(t, k8sClient, argoSecret)

	assert.Equal(t, clusterName, string(argoSecret.Data["name"]))
	assert.Equal(t, serverURL, string(argoSecret.Data["server"]))
	assert.JSONEq(t, expectedJsonConfig, string(argoSecret.Data["config"]))
}

func TestClusterAPIClusterReconcilerCreatesArgoCDCluster(t *testing.T) {
	t.Run("When the authentication is done through a token", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithBearer("token"), `{"bearerToken":"token"}`)
	})
	t.Run("When the authentication is done through a client certificate, with insecure TLS", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithClientCertificatData([]byte("certdata")).WithClientKeyData([]byte("keydata")).WithInsecure(true), `{ "tlsClientConfig": {"certData":"Y2VydGRhdGE=", "insecure":true, "keyData":"a2V5ZGF0YQ=="}}`)
	})
	t.Run("When the authentication is done through a client certificate", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithClientCertificatData([]byte("certdata")).WithClientKeyData([]byte("keydata")), `{ "tlsClientConfig": {"certData":"Y2VydGRhdGE=", "keyData":"a2V5ZGF0YQ=="}}`)
	})
	t.Run("When the authentication is done through a token, with insecure TLS", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithBearer("token").WithInsecure(true), `{"bearerToken":"token", "tlsClientConfig": {"insecure": true}}`)
	})
	t.Run("When the authentication is done through a token", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithBearer("token").WithCACertData([]byte("hello world")), `{"bearerToken":"token", "tlsClientConfig": {"caData": "aGVsbG8gd29ybGQ="}}`)
	})
	t.Run("When the authentication is done through a token, with insecure TLS", func(t *testing.T) {
		clusterAPIClusterReconcilerCreatesArgoCDClusterTestCase(t, NewCAPIArgoCDSecretBuilder().WithBearer("token").WithCACertData([]byte("hello world")).WithInsecure(true), `{"bearerToken":"token", "tlsClientConfig": {"insecure": true, "caData": "aGVsbG8gd29ybGQ="}}`)
	})
}

func testPredicateTestCase(t *testing.T, secret *v1.Secret, expected bool) {
	t.Helper()
	p := isClusterSecret{}
	t.Run(fmt.Sprintf("predicates should return %v on Create", expected), func(t *testing.T) {
		assert.Equal(t, expected, p.Create(event.CreateEvent{Object: secret}))
	})
	t.Run(fmt.Sprintf("predicates should return %v on Update", expected), func(t *testing.T) {
		assert.Equal(t, expected, p.Update(event.UpdateEvent{ObjectNew: secret}))
	})
	t.Run(fmt.Sprintf("predicates should return %v on Delete", expected), func(t *testing.T) {
		assert.Equal(t, expected, p.Delete(event.DeleteEvent{Object: secret}))
	})
	t.Run("predicate should always return true for generic events", func(t *testing.T) {
		assert.Equal(t, false, p.Generic(event.GenericEvent{}))
	})
}

func TestPredicate(t *testing.T) {
	t.Run("When the secret has no labels", func(t *testing.T) {
		testPredicateTestCase(t, &v1.Secret{}, false)
	})
	t.Run("When the secret is another argocd secret type", func(t *testing.T) {
		testPredicateTestCase(t, NewArgoCDClusterSecretBuilder().WithArgoCDSecretType("other").Build(), false)
	})

	t.Run("When the secret is an argocd cluster secret type", func(t *testing.T) {
		testPredicateTestCase(t, NewArgoCDClusterSecretBuilder().Build(), true)
	})

	t.Run("When the is a cluster-api config secret", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "test-argocd",
			},
		}
		testPredicateTestCase(
			t,
			NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t),
			true,
		)
	})

	t.Run("When the is a cluster-api config secret for a different purpose", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "test-argocd",
			},
		}
		testPredicateTestCase(
			t,
			// capa generates ${cluster}-kubeconfig and ${cluster}-user-kubeconfig
			NewCAPIArgoCDSecretBuilder().WithCluster(cluster).WithPurpose(secret.Purpose("user-kubeconfig")).Build(t),
			false,
		)
	})
	t.Run("When the is a cluster-api config secret for a different purpose", func(t *testing.T) {
		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "test-argocd",
			},
		}
		testPredicateTestCase(
			t,
			// capa generates ${cluster}-kubeconfig and ${cluster}-user-kubeconfig
			NewCAPIArgoCDSecretBuilder().WithCluster(cluster).WithPurpose(secret.FrontProxyCA).Build(t),
			false,
		)
	})

}

func TestErrorCases(t *testing.T) {
	t.Run("When the k8s config data is invalid", func(t *testing.T) {
		ctx := context.TODO()
		clusterName := "my-cluster"
		argoCDNamespace := "test-argocd"

		cluster := &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: argoCDNamespace,
			},
		}

		clusterSecret := NewCAPIArgoCDSecretBuilder().WithCluster(cluster).Build(t)
		clusterSecret.Data[secret.KubeconfigDataName] = []byte("invalid-data")
		k8sClient := fake.NewClientBuilder().
			WithScheme(Scheme()).
			WithObjects(clusterSecret, cluster).
			Build()

		reconciler := &ClusterReconciler{
			Client:           k8sClient,
			ArgoCDNamespace:  argoCDNamespace,
			AnnotationPrefix: testAnnotationPrefix,
			EnableByDefault:  true,
		}

		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		require.Error(t, err)
		assert.Equal(t, ctrl.Result{}, result)
		assert.Contains(t, err.Error(), "failed to create REST configuration for Cluster")
	})
}
