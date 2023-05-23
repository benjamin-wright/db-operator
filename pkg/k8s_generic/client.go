package k8s_generic

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type Resource[T any] interface {
	*T
	GetName() string
	GetUID() string
	GetResourceVersion() string
	ToUnstructured(namespace string) *unstructured.Unstructured
	FromUnstructured(obj *unstructured.Unstructured) error
	Equal(obj T) bool
}

type Client[T any, PT Resource[T]] struct {
	client       dynamic.Interface
	restClient   *rest.RESTClient
	namespace    string
	schema       schema.GroupVersionResource
	kind         string
	labelFilters map[string]interface{}
}

func New[T any, PT Resource[T]](resourceSchema schema.GroupVersionResource, kind string, namespace string, labelFilters map[string]interface{}) (*Client[T, PT], error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	var config *rest.Config
	var err error

	if kubeconfig != "" {
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

	return &Client[T, PT]{
		namespace:    namespace,
		schema:       resourceSchema,
		client:       dynClient,
		restClient:   restClient,
		labelFilters: labelFilters,
		kind:         kind,
	}, nil
}

func (c *Client[T, PT]) Create(ctx context.Context, resource T) error {
	ptr := PT(&resource)

	_, err := c.client.Resource(c.schema).Namespace(c.namespace).Create(ctx, ptr.ToUnstructured(c.namespace), v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create %T: %+v", resource, err)
	}

	return nil
}

func (c *Client[T, PT]) Delete(ctx context.Context, name string) error {
	err := c.client.Resource(c.schema).Namespace(c.namespace).Delete(ctx, name, v1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete %T: %+v", name, err)
	}

	return nil
}

func (c *Client[T, PT]) DeleteAll(ctx context.Context) error {
	err := c.client.Resource(c.schema).Namespace(c.namespace).DeleteCollection(ctx, v1.DeleteOptions{}, v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete all resources: %+v", err)
	}

	return nil
}

func (c *Client[T, PT]) Update(ctx context.Context, resource T) error {
	ptr := PT(&resource)

	_, err := c.client.Resource(c.schema).Namespace(c.namespace).Update(ctx, ptr.ToUnstructured(c.namespace), v1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed up update resource %s: %+v", ptr.GetName(), err)
	}

	return nil
}

type Update[T any] struct {
	ToAdd    []T
	ToRemove []T
}

func (c *Client[T, PT]) Watch(ctx context.Context, cancel context.CancelFunc) (<-chan Update[T], error) {
	output := make(chan Update[T], 1)
	ignore := func(obj interface{}) bool {
		if c.labelFilters == nil {
			return false
		}

		u := obj.(*unstructured.Unstructured)

		for key, value := range c.labelFilters {
			label, err := GetProperty[string](u, "metadata", "labels", key)
			if err != nil {
				return true
			}

			if label != value {
				return true
			}
		}

		return false
	}

	convert := func(obj interface{}) T {
		var res T
		ptr := PT(&res)
		err := ptr.FromUnstructured(obj.(*unstructured.Unstructured))
		if err != nil {
			zap.S().Errorf("Failed to parse unstructured obj for %T: %+v\n %+v", res, err, obj)
		}
		return res
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.client, time.Minute, c.namespace, nil)
	informer := factory.ForResource(c.schema).Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if ignore(obj) {
				return
			}

			res := convert(obj)

			output <- Update[T]{
				ToAdd:    []T{res},
				ToRemove: []T{},
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if ignore(oldObj) || ignore(newObj) {
				return
			}

			oldRes := convert(oldObj)
			newRes := convert(newObj)

			if !PT(&oldRes).Equal(newRes) {
				output <- Update[T]{
					ToAdd:    []T{newRes},
					ToRemove: []T{oldRes},
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ignore(obj) {
				return
			}

			res := convert(obj)

			output <- Update[T]{
				ToAdd:    []T{},
				ToRemove: []T{res},
			}
		},
	})

	go func() {
		informer.Run(ctx.Done())
		if ctx.Err() == nil {
			cancel()
		}
	}()

	return output, nil
}

func (c *Client[T, PT]) Event(ctx context.Context, obj T, eventtype, reason, message string) {
	ptr := PT(&obj)

	ref := corev1.ObjectReference{
		Kind:            c.kind,
		APIVersion:      c.schema.Group + "/" + c.schema.Version,
		Name:            ptr.GetName(),
		Namespace:       c.namespace,
		UID:             types.UID(ptr.GetUID()),
		ResourceVersion: ptr.GetResourceVersion(),
	}

	t := v1.Time{Time: time.Now()}

	e := &corev1.Event{
		ObjectMeta: v1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", ref.Name, t.UnixNano()),
			Namespace: c.namespace,
		},
		InvolvedObject: ref,
		Reason:         reason,
		Message:        message,
		FirstTimestamp: t,
		LastTimestamp:  t,
		Count:          1,
		Source:         corev1.EventSource{Component: "ponglehub.co.uk/db-operator"},
		Type:           eventtype,
	}

	err := c.restClient.Post().
		Namespace(c.namespace).
		Resource("events").
		Body(e).
		Do(ctx).
		Into(nil)

	if err != nil {
		zap.S().Warnf("Failed to publish event: %+v", err)
	}
}
