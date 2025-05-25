package kube

import (
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = tektonv1.AddToScheme(scheme)
}

// GetKubeClient creates and returns a new Kubernetes client from controller-runtime.
// It uses the default kubeconfig resolution (e.g., from ~/.kube/config or in-cluster config).
func GetKubeClient() (client.Client, error) {
	kcfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	k8sClient, err := client.New(kcfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	return k8sClient, nil
}
