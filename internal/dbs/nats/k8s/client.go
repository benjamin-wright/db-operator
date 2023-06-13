package k8s

import (
	"context"
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
)

type K8sClient[T any] interface {
	Watch(ctx context.Context, cancel context.CancelFunc) (<-chan k8s_generic.Update[T], error)
	Create(ctx context.Context, resource T) error
	Delete(ctx context.Context, name string) error
	Update(ctx context.Context, resource T) error
	Event(ctx context.Context, obj T, eventtype, reason, message string)
}

type Client struct {
	builder *k8s_generic.Builder
}

func New(namespace string) (*Client, error) {
	builder, err := k8s_generic.NewBuilder(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s builder: %w", err)
	}

	return &Client{
		builder: builder,
	}, nil
}
