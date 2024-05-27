package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clients"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/clusters"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/pvcs"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/secrets"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/services"
	"github.com/benjamin-wright/db-operator/internal/dbs/postgres/k8s/stateful_sets"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
)

type Client struct {
	clients      *k8s_generic.Client[clients.Resource]
	clusters     *k8s_generic.Client[clusters.Resource]
	pvcs         *k8s_generic.Client[pvcs.Resource]
	secrets      *k8s_generic.Client[secrets.Resource]
	services     *k8s_generic.Client[services.Resource]
	statefulsets *k8s_generic.Client[stateful_sets.Resource]
}

func New() (*Client, error) {
	builder, err := k8s_generic.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s builder: %+v", err)
	}

	return &Client{
		clients:      k8s_generic.NewClient(builder, clients.ClientArgs),
		clusters:     k8s_generic.NewClient(builder, clusters.ClientArgs),
		pvcs:         k8s_generic.NewClient(builder, pvcs.ClientArgs),
		secrets:      k8s_generic.NewClient(builder, secrets.ClientArgs),
		services:     k8s_generic.NewClient(builder, services.ClientArgs),
		statefulsets: k8s_generic.NewClient(builder, stateful_sets.ClientArgs),
	}, nil
}

func (c *Client) Clients() *k8s_generic.Client[clients.Resource] {
	return c.clients
}

func (c *Client) Clusters() *k8s_generic.Client[clusters.Resource] {
	return c.clusters
}

func (c *Client) PVCs() *k8s_generic.Client[pvcs.Resource] {
	return c.pvcs
}

func (c *Client) Secrets() *k8s_generic.Client[secrets.Resource] {
	return c.secrets
}

func (c *Client) Services() *k8s_generic.Client[services.Resource] {
	return c.services
}

func (c *Client) StatefulSets() *k8s_generic.Client[stateful_sets.Resource] {
	return c.statefulsets
}
