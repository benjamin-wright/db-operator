package k8s_generic

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type Resource interface {
	GetName() string
	GetNamespace() string
	GetUID() string
	GetResourceVersion() string
	ToUnstructured() *unstructured.Unstructured
	Equal(obj Resource) bool
}

type FromUnstructured[T Resource] func(obj *unstructured.Unstructured) (T, error)

type Client[T Resource] struct {
	client           dynamic.Interface
	restClient       *rest.RESTClient
	schema           schema.GroupVersionResource
	kind             string
	labelFilters     map[string]string
	fromUnstructured FromUnstructured[T]
}

type ClientArgs[T Resource] struct {
	Schema           schema.GroupVersionResource
	Kind             string
	LabelFilters     map[string]string
	FromUnstructured FromUnstructured[T]
}

func NewClient[T Resource](b *Builder, args ClientArgs[T]) *Client[T] {
	return &Client[T]{
		schema:           args.Schema,
		client:           b.dynClient,
		restClient:       b.restClient,
		labelFilters:     args.LabelFilters,
		kind:             args.Kind,
		fromUnstructured: args.FromUnstructured,
	}
}

func (c *Client[T]) Create(ctx context.Context, resource T) error {
	_, err := c.client.Resource(c.schema).Namespace(resource.GetNamespace()).Create(ctx, resource.ToUnstructured(), v1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create %T: %+v", resource, err)
	}

	return nil
}

func (c *Client[T]) Get(ctx context.Context, name string, namespace string) (T, error) {
	res, err := c.client.Resource(c.schema).Namespace(namespace).Get(ctx, name, v1.GetOptions{})
	if err != nil {
		var object T
		return object, fmt.Errorf("failed to get %T: %s", object, name)
	}

	object, err := c.fromUnstructured(res)
	if err != nil {
		return object, fmt.Errorf("failed to parse %T: %+v", object, err)
	}

	return object, nil
}

// function to get all resources
func (c *Client[T]) GetAll(ctx context.Context) ([]T, error) {
	var objects []T
	options := v1.ListOptions{}
	if len(c.labelFilters) > 0 {
		options.LabelSelector = v1.FormatLabelSelector(&v1.LabelSelector{
			MatchLabels: c.labelFilters,
		})
	}

	res, err := c.client.Resource(c.schema).List(ctx, options)
	if err != nil {
		return objects, fmt.Errorf("failed to get all %T: %+v", objects, err)
	}

	for _, item := range res.Items {
		object, err := c.fromUnstructured(&item)
		if err != nil {
			return objects, fmt.Errorf("failed to parse %T: %+v", objects, err)
		}

		objects = append(objects, object)
	}

	return objects, nil
}

func (c *Client[T]) Delete(ctx context.Context, name string, namespace string) error {
	err := c.client.Resource(c.schema).Namespace(namespace).Delete(ctx, name, v1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete %T: %+v", name, err)
	}

	return nil
}

func (c *Client[T]) DeleteAll(ctx context.Context, namespace string) error {
	err := c.client.Resource(c.schema).Namespace(namespace).DeleteCollection(ctx, v1.DeleteOptions{}, v1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete all resources: %+v", err)
	}

	return nil
}

func (c *Client[T]) Update(ctx context.Context, resource T) error {
	_, err := c.client.Resource(c.schema).Namespace(resource.GetNamespace()).Update(ctx, resource.ToUnstructured(), v1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed up update resource %s: %+v", resource.GetName(), err)
	}

	return nil
}

type Update[T any] struct {
	ToAdd    []T
	ToRemove []T
}

func (c *Client[T]) Watch(ctx context.Context, cancel context.CancelFunc, updates chan<- any) error {
	convert := func(obj interface{}) T {
		res, err := c.fromUnstructured(obj.(*unstructured.Unstructured))
		if err != nil {
			log.Error().Err(err).Msgf("Failed to parse unstructured obj for %T", res)
		}
		return res
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.client, time.Minute, "", func(lo *v1.ListOptions) {
		if len(c.labelFilters) == 0 {
			return
		}

		lo.LabelSelector = v1.FormatLabelSelector(&v1.LabelSelector{
			MatchLabels: c.labelFilters,
		})
	})
	informer := factory.ForResource(c.schema).Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			res := convert(obj)

			updates <- Update[T]{
				ToAdd:    []T{res},
				ToRemove: []T{},
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldRes := convert(oldObj)
			newRes := convert(newObj)

			if !oldRes.Equal(newRes) {
				updates <- Update[T]{
					ToAdd:    []T{newRes},
					ToRemove: []T{oldRes},
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			res := convert(obj)

			updates <- Update[T]{
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

	return nil
}

func (c *Client[T]) Event(ctx context.Context, obj T, eventtype, reason, message string) {
	ref := corev1.ObjectReference{
		Kind:            c.kind,
		APIVersion:      c.schema.Group + "/" + c.schema.Version,
		Name:            obj.GetName(),
		Namespace:       obj.GetNamespace(),
		UID:             types.UID(obj.GetUID()),
		ResourceVersion: obj.GetResourceVersion(),
	}

	t := v1.Time{Time: time.Now()}

	e := &corev1.Event{
		ObjectMeta: v1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", ref.Name, t.UnixNano()),
			Namespace: obj.GetNamespace(),
		},
		InvolvedObject: ref,
		Reason:         reason,
		Message:        message,
		FirstTimestamp: t,
		LastTimestamp:  t,
		Count:          1,
		Source:         corev1.EventSource{Component: "github.com/benjamin-wright/db-operator"},
		Type:           eventtype,
	}

	err := c.restClient.Post().
		Namespace(obj.GetNamespace()).
		Resource("events").
		Body(e).
		Do(ctx).
		Into(nil)

	if err != nil {
		log.Error().Err(err).Msg("Failed to publish event")
	}
}
