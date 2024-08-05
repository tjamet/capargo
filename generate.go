package clusterapiaddonproviderargocd

//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0 rbac:roleName=controller-role output:rbac:artifacts:config=config/rbac paths="./..."
//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0 object:headerFile="hack/boilerplate.go.txt" paths="./..."
