/*


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

package controllers

import (
	"context"
	"encoding/json"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	"sigs.k8s.io/cluster-api/util/secret"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type AuthenticationSettings struct {
	Username           string              `json:"username,omitempty" yaml:"username,omitempty"`
	Password           string              `json:"password,omitempty" yaml:"password,omitempty"`
	BearerToken        string              `json:"bearerToken,omitempty" yaml:"bearerToken,omitempty"`
	AWSAuthConfig      *AWSAuthConfig      `json:"awsAuthConfig,omitempty" yaml:"awsAuthConfig,omitempty"`
	ExecProviderConfig *ExecProviderConfig `json:"execProviderConfig,omitempty" yaml:"execProviderConfig,omitempty"`
	TLSClientConfig    *TLSClientConfig    `json:"tlsClientConfig,omitempty" yaml:"tlsClientConfig,omitempty"`
}

func (a *AuthenticationSettings) GetTLSClientConfig() *TLSClientConfig {
	if a.TLSClientConfig == nil {
		a.TLSClientConfig = &TLSClientConfig{}
	}
	return a.TLSClientConfig
}

type AWSAuthConfig struct {
	ClusterName string `json:"clusterName,omitempty" yaml:"clusterName,omitempty"`
	RoleARN     string `json:"roleARN,omitempty" yaml:"roleARN,omitempty"`
	Profile     string `json:"profile,omitempty" yaml:"profile,omitempty"`
}

type ExecProviderConfig struct {
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`
	Args        []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	APIVersion  string            `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	InstallHint string            `json:"installHint,omitempty" yaml:"installHint,omitempty"`
}

type TLSClientConfig struct {
	CAData     []byte `json:"caData,omitempty" yaml:"caData,omitempty"`
	CertData   []byte `json:"certData,omitempty" yaml:"certData,omitempty"`
	Insecure   bool   `json:"insecure,omitempty" yaml:"insecure,omitempty"`
	KeyData    []byte `json:"keyData,omitempty" yaml:"keyData,omitempty"`
	ServerName string `json:"serverName,omitempty" yaml:"serverName,omitempty"`
}

// ClusterReconciler reconciles argocd cluster secrets to ensure they match cluster objects
// It acts as a garbage collector for possible deleted clusters while the cluster sync is not running
type ClusterReconciler struct {
	client.Client
	ArgoCDNamespace  string
	AnnotationPrefix string
	EnableByDefault  bool
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch

func restConfigToArgoCDConfig(restConfig *rest.Config) AuthenticationSettings {
	configData := AuthenticationSettings{}
	if restConfig.CAData != nil {
		configData.GetTLSClientConfig().CAData = restConfig.CAData
	}
	if restConfig.Insecure {
		configData.GetTLSClientConfig().Insecure = true
	}

	if restConfig.CertData != nil {
		configData.GetTLSClientConfig().CertData = restConfig.CertData
	}
	if restConfig.KeyData != nil {
		configData.GetTLSClientConfig().KeyData = restConfig.KeyData
	}

	if restConfig.BearerToken != "" {
		configData.BearerToken = restConfig.BearerToken
	}
	return configData
}
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = klog.NewContext(
		ctx,
		klog.FromContext(ctx).WithName("ClusterReconciler").WithValues("reconcileRequest", req, "reconciler", "ClusterReconciler", "argocdNamespace", r.ArgoCDNamespace),
	)
	cluster := &capiv1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
	}
	argoCDClusterSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.ArgoCDNamespace,
			Name:      argoCDSecretName(cluster.Name),
		},
	}

	if argoCDClusterSecret.Namespace == "" {
		argoCDClusterSecret.Namespace = cluster.Namespace
	}

	foundCluster := true

	err := r.Client.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
	if err != nil && apierrors.IsNotFound(err) {
		klog.FromContext(ctx).V(4).Info("cluster object not found. This will trigger deletion of the argocd cluster secret")
		foundCluster = false
	}
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	enabled := r.EnableByDefault
	mode, ok := cluster.GetLabels()[annotation(r.AnnotationPrefix, CapargoArgoCD)]
	if ok {
		enabled = isEnabled(mode)
	}
	if !enabled {
		klog.FromContext(ctx).Info("cluster is disabled. Skipping cluster")
		return ctrl.Result{}, nil
	}

	// Fetch the kubeconfig for this specific cluster object
	restConfig, err := remote.RESTConfig(ctx, "capi-argo", r.Client, client.ObjectKeyFromObject(cluster))
	if err != nil && apierrors.IsNotFound(err) {
		klog.FromContext(ctx).V(4).Info("cluster object not found. This will trigger deletion of the argocd cluster secret")
		foundCluster = false
	}
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	if !foundCluster {
		klog.FromContext(ctx).Info("cluster or kubeconfig not found. Deleting argocd cluster secret")
		err = r.Client.Delete(ctx, argoCDClusterSecret)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	ctx = klog.NewContext(
		ctx,
		klog.FromContext(ctx).WithValues("argoSecretName", argoCDClusterSecret.Name),
	)

	klog.FromContext(ctx).WithValues("clusterSecretNamespace", argoCDClusterSecret.Namespace, "clusterSecretName", argoCDClusterSecret.Name).Info("upserting cluster secret")
	_, err = ctrl.CreateOrUpdate(context.Background(), r.Client, argoCDClusterSecret, func() error {
		argoCDClusterSecret.Labels = map[string]string{}
		for k, v := range cluster.Labels {
			argoCDClusterSecret.Labels[k] = v
		}
		argoCDClusterSecret.Labels[ArgoCDSecretTypeLabelName] = ArgoCDSecretTypeCluster

		if argoCDClusterSecret.Annotations == nil {
			argoCDClusterSecret.Annotations = map[string]string{}
		}
		argoCDClusterSecret.Annotations[annotation(r.AnnotationPrefix, ClusterNameAnnotationSuffix)] = cluster.Name
		argoCDClusterSecret.Annotations[annotation(r.AnnotationPrefix, ClusterNamespaceAnnotationSuffix)] = cluster.Namespace

		config, err := json.Marshal(restConfigToArgoCDConfig(restConfig))
		if err != nil {
			return err
		}
		argoCDClusterSecret.Data = map[string][]byte{
			"config": config,
			"name":   []byte(cluster.Name),
			"server": []byte(restConfig.Host),
		}
		return nil
	})
	if err != nil {
		klog.FromContext(ctx).Error(err, "failed to upsert argocd cluster")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func reconcileReqFromArgoCDCluster(ctx context.Context, annotationPrefix string, secret client.Object) *ctrl.Request {
	clusterObjectNamespace := secret.GetAnnotations()[annotation(annotationPrefix, ClusterNamespaceAnnotationSuffix)]
	clusterObjectName := secret.GetAnnotations()[annotation(annotationPrefix, ClusterNameAnnotationSuffix)]

	if clusterObjectNamespace == "" || clusterObjectName == "" {
		klog.FromContext(ctx).Info("cluster object name or namespace not found in secret annotations. Skipping")
		return nil
	}
	return &ctrl.Request{NamespacedName: types.NamespacedName{Namespace: clusterObjectNamespace, Name: clusterObjectName}}
}

func reconcileReqFromClusterAPISecret(ctx context.Context, secret client.Object) *ctrl.Request {
	clusterName, ok := secret.GetLabels()[clusterv1.ClusterNameLabel]
	if !ok {
		klog.FromContext(ctx).Info("cluster name not found in secret labels. Skipping secret")
		return nil
	}
	return &ctrl.Request{NamespacedName: types.NamespacedName{Name: clusterName, Namespace: secret.GetNamespace()}}
}

func reconcileReqFromSecret(ctx context.Context, annotationPrefix, argoCDNamespace string, secret client.Object) *ctrl.Request {
	if isArgoCDClusterObject(secret) {
		if argoCDNamespace != "" && secret.GetNamespace() != argoCDNamespace {
			return nil
		}
		return reconcileReqFromArgoCDCluster(ctx, annotationPrefix, secret)
	}
	if isClusterAPIConfigSecret(secret) {
		return reconcileReqFromClusterAPISecret(ctx, secret)
	}
	return nil
}

type isClusterSecret struct{}

func isArgoCDClusterObject(obj client.Object) bool {
	isArgo := obj.GetLabels()[ArgoCDSecretTypeLabelName] == ArgoCDSecretTypeCluster
	if !isArgo {
		klog.FromContext(context.Background()).V(5).WithName("secret-predicate").WithValues("objectRef", client.ObjectKeyFromObject(obj)).Info("secret is not an argocd cluster secret")
	}
	return isArgo
}

func isClusterAPIConfigSecret(obj client.Object) bool {
	parsedClusterName, purpose, err := secret.ParseSecretName(obj.GetName())
	if err != nil {
		klog.FromContext(context.Background()).V(5).WithName("secret-predicate").WithValues("objectRef", client.ObjectKeyFromObject(obj)).Error(err, "failed to parse secret name")
		return false
	}
	if purpose != secret.Kubeconfig {
		klog.FromContext(context.Background()).V(5).WithName("secret-predicate").WithValues("objectRef", client.ObjectKeyFromObject(obj), "purpose", purpose, "expectedPurpose", secret.Kubeconfig).Info("failed to parse secret name")
		return false
	}
	clusterNameFromLabels, isClusterApiSecret := obj.GetLabels()[clusterv1.ClusterNameLabel]
	if !isClusterApiSecret {
		klog.FromContext(context.Background()).V(5).WithName("secret-predicate").WithValues("objectRef", client.ObjectKeyFromObject(obj)).Info("secret is not a cluster API secret")
	}
	if parsedClusterName != clusterNameFromLabels {
		klog.FromContext(context.Background()).V(5).WithName("secret-predicate").WithValues("objectRef", client.ObjectKeyFromObject(obj), "parsedClusterName", parsedClusterName, "clusterNameFromLabels", clusterNameFromLabels).Info("secret name does not match cluster name label")
		return false
	}
	return isClusterApiSecret
}

var _ predicate.Predicate = isClusterSecret{}

func (isClusterSecret) Create(e event.CreateEvent) bool {
	return isArgoCDClusterObject(e.Object) || isClusterAPIConfigSecret(e.Object)
}

func (isClusterSecret) Update(e event.UpdateEvent) bool {
	return isArgoCDClusterObject(e.ObjectNew) || isClusterAPIConfigSecret(e.ObjectNew)
}

func (isClusterSecret) Delete(e event.DeleteEvent) bool {
	return isArgoCDClusterObject(e.Object) || isClusterAPIConfigSecret(e.Object)
}

func (isClusterSecret) Generic(event.GenericEvent) bool {
	return false
}

type clusterSecretEventHandler struct {
	AnnotationPrefix string
	ArgoCDNamespace  string
}

var _ handler.EventHandler = &clusterSecretEventHandler{}

func (c *clusterSecretEventHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
	klog.FromContext(ctx).V(5).WithName("clusterSecretEventHandler").WithValues("event", e).Info("create event")
	req := reconcileReqFromSecret(ctx, c.AnnotationPrefix, c.ArgoCDNamespace, e.Object)
	if req != nil {
		q.AddRateLimited(*req)
	}
}

func (c *clusterSecretEventHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	klog.FromContext(ctx).V(5).WithName("clusterSecretEventHandler").WithValues("event", e).Info("update event")
	req := reconcileReqFromSecret(ctx, c.AnnotationPrefix, c.ArgoCDNamespace, e.ObjectNew)
	if req != nil {
		q.AddRateLimited(*req)
	}
}

func (c *clusterSecretEventHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	klog.FromContext(ctx).V(5).WithName("clusterSecretEventHandler").WithValues("event", e).Info("delete event")
	req := reconcileReqFromSecret(ctx, c.AnnotationPrefix, c.ArgoCDNamespace, e.Object)
	if req != nil {
		q.AddRateLimited(*req)
	}
}

func (c *clusterSecretEventHandler) Generic(ctx context.Context, e event.GenericEvent, q workqueue.RateLimitingInterface) {
	klog.FromContext(ctx).V(5).WithName("clusterSecretEventHandler").WithValues("event", e).Info("generic event")
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capiv1beta1.Cluster{}).
		// Watch changes on secrets as well.
		// A Cluster may not be updated when a kubeconfig changes.
		// Shall an argocd cluster secret be updated, ensure its credentials were not deleted
		// All those will trigger a reconcile on the capi cluster object
		// Allowing to handle cases where:
		// 1. The cluster is being created
		// 2. The cluster is being updated
		// 3. The cluster is being deleted
		// All those limiting the amount of reconciliation loops thanks to the rate limiting queue.
		Watches(&v1.Secret{}, &clusterSecretEventHandler{AnnotationPrefix: r.AnnotationPrefix, ArgoCDNamespace: r.ArgoCDNamespace}, builder.WithPredicates(isClusterSecret{})).
		Complete(r)
}
