package utils

import (
	"bytes"

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

/*
* Function to remove the encoding bytes from a file.
* @param file The file to remove the encoding from.
 */
func TrapBOM(file []byte) []byte {
	return bytes.Trim(file, "\xef\xbb\xbf")
}
