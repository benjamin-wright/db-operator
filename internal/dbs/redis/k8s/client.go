package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
)

type Client struct {
	builder *k8s_generic.Builder
}

func New() (*Client, error) {
	builder, err := k8s_generic.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s builder: %+v", err)
	}

	return &Client{
		builder: builder,
	}, nil
}
