package k8s_generic

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Builder struct {
	config     *rest.Config
	dynClient  *dynamic.DynamicClient
	restClient *rest.RESTClient
}

func NewBuilder() (*Builder, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %+v", err)
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	config.GroupVersion = &schema.GroupVersion{
		Group:   "api",
		Version: "v1",
	}
	config.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{CodecFactory: scheme.Codecs}

	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}

	return &Builder{
		dynClient:  dynClient,
		restClient: restClient,
		config:     config,
	}, nil
}
