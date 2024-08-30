package utils

import (
	"context"

	finopsDataTypes "github.com/krateoplatformops/finops-data-types/api/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func GetClientSet() (*kubernetes.Clientset, error) {
	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		return &kubernetes.Clientset{}, err
	}

	inClusterConfig.APIPath = "/apis"
	inClusterConfig.GroupVersion = &schema.GroupVersion{Group: "finops.krateo.io", Version: "v1"}

	clientset, err := kubernetes.NewForConfig(inClusterConfig)
	if err != nil {
		return &kubernetes.Clientset{}, err
	}
	return clientset, nil
}

func GetBearerTokenSecret(config finopsDataTypes.ExporterScraperConfig) (string, error) {
	clientset, err := GetClientSet()
	if err != nil {
		return "", err
	}

	secret, err := clientset.CoreV1().Secrets(config.Spec.ExporterConfig.BearerToken.Namespace).Get(context.TODO(), config.Spec.ExporterConfig.BearerToken.Name, v1.GetOptions{})
	if err != nil {
		return "", err
	}
	return string(secret.Data["bearer-token"]), nil
}
