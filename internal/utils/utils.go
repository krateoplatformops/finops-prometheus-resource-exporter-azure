package utils

import (
	"bytes"
	"os"
	"regexp"
	"strings"

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

// replaceVariables replaces all variables in the format <variable> with their values
// from the additionalVariables map or from environment variables if the variable name is uppercase
func ReplaceVariables(text string, additionalVariables map[string]string) string {
	regex, _ := regexp.Compile("<.*?>")
	toReplaceRange := regex.FindStringIndex(text)

	for toReplaceRange != nil {
		// Extract variable name without the < > brackets
		varName := text[toReplaceRange[0]+1 : toReplaceRange[1]-1]

		// Get replacement value from additionalVariables
		varToReplace := additionalVariables[varName]

		// If the variable name is all uppercase, get value from environment
		if varToReplace == strings.ToUpper(varToReplace) {
			varToReplace = os.Getenv(varToReplace)
		}

		// Replace the variable in the text
		text = strings.Replace(text, text[toReplaceRange[0]:toReplaceRange[1]], varToReplace, -1)

		// Find next variable
		toReplaceRange = regex.FindStringIndex(text)
	}

	return text
}
