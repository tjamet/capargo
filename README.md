# Capargo: Yet another capi to argo secret reconciler

Capargo is yet another project to handle the creation of argocd cluster secrets based on capi cluster objects.

⚠️ This project is, so far, an experiment and may be moved or removed without any prior notice.

## Main differences from other implementations

| Project | Listener(s) | ClusterDeletion | Labels | Opt-out | Projects |
| --- | --- | --- | --- | --- | --- |
| Capargo | Clusters and Secrets | Handled | From Cluster object | Trough Cluster labels | Not managed |
| [argocd-cluster-register](https://github.com/dmolik/argocd-cluster-register) | Clusters | | static | Not implemented | Managed |
| [argocdsecretsynchronizer](https://github.com/a1tan/argocdsecretsynchronizer) | Custom CRD and Secret | | static | Through CRD | Not managed |
| [capi2argo-cluster-operator](https://github.com/dntosas/capi2argo-cluster-operator) | Secrets | Handled | Prefixed from cluster object | Not implemented | Not managed |

This project differs from previous implementation as it allows to simply opt-in or opt-out argocd cluster creation, while keeping a simple interface to assign labels to ArgoCD cluster objects, enabling [ApplicationSet generators].

It also ensures that the argocd cluster object is kept up-to-date with the latest available kubeconfig and prevents from manual undesired modifications of the ArgoCD cluster object. In particular:

1. credentials are enforced. If the argocd cluster object credentials are changed, its credentials will be restored from the kubeconfig
2. labels are enforced. If the argocd cluster labels are changed, they will be restored from the cluster object.

## Workflow

For each cluster object, a matching argocd cluster secret is created, as soon as a kubeconfig has been created by the provisioner of this cluster.

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
    namespace: default
    name: my-cluster
    labels:
        some: label
```

will generate, by default, an argocd cluster object

```yaml
apiVersion: v1
kind: Secret
metadata:
    namespace: argocd
    name: capargo-default-my-cluster
    labels: # New labels can't be added
        some: label
        argocd.argoproj.io/secret-type: cluster
    annotations:
        capargo.jamet.dev/cluster-object-name: thibus # This can't be changed
        capargo.jamet.dev/cluster-object-namespace: default # This can't be changed
```

## Customization

Capargo accepts some customzation.

### Metadata prefix

You can change the default `capargo.jamet.dev` annotation and labels prefix using the command line argument `--metadata-prefix=my-prefix`.

The resulting argocd object will be

```yaml
apiVersion: v1
kind: Secret
metadata:
    namespace: argocd
    name: capargo-default-my-cluster
    labels: # New labels can't be added
        some: label
        argocd.argoproj.io/secret-type: cluster
    annotations:
        my-prefix/cluster-object-name: thibus # This can't be changed
        my-prefix/cluster-object-namespace: default # This can't be changed
```

### Enabling or disabling at the cluster level

You can enable or disable the integration at the cluster level adding the label `capargo.jamet.dev/argocd: (enable|disable)` to the cluster object.

In practice, any value different from `enable` or `enabled` will disable the creation of argocd cluster secret 

✍️ Note that if you changed the metadata prefix using `--metadata-prefix=my-prefix` the label becomes `my-prefix/argocd: (enable|disable)`

### Changing the default behaviour

By default, capargo will create argocd cluster secrets for all clusters.
You can change this behaviour and disable by default using the command line argument `--enable-by-default=false`.

✍️ In this case, only clusters with a label `capargo.jamet.dev/argocd: (enable|enabled)` will have a matching argocd cluster created.

[ApplicationSet generators]: https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/Generators-Cluster/#label-selector