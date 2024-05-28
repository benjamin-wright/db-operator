package k8s

import (
	"fmt"

	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clients"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/clusters"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/deployments"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/secrets"
	"github.com/benjamin-wright/db-operator/v2/internal/dbs/nats/k8s/services"
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
)

type Client struct {
	clients     *k8s_generic.Client[clients.Resource]
	clusters    *k8s_generic.Client[clusters.Resource]
	secrets     *k8s_generic.Client[secrets.Resource]
	deployments *k8s_generic.Client[deployments.Resource]
	services    *k8s_generic.Client[services.Resource]
}

func New() (*Client, error) {
	builder, err := k8s_generic.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s builder: %+v", err)
	}

	return &Client{
		clients:     k8s_generic.NewClient(builder, clients.ClientArgs),
		clusters:    k8s_generic.NewClient(builder, clusters.ClientArgs),
		secrets:     k8s_generic.NewClient(builder, secrets.ClientArgs),
		deployments: k8s_generic.NewClient(builder, deployments.ClientArgs),
		services:    k8s_generic.NewClient(builder, services.ClientArgs),
	}, nil
}

func (c *Client) Clients() *k8s_generic.Client[clients.Resource] {
	return c.clients
}

func (c *Client) Clusters() *k8s_generic.Client[clusters.Resource] {
	return c.clusters
}

func (c *Client) Secrets() *k8s_generic.Client[secrets.Resource] {
	return c.secrets
}

func (c *Client) Deployments() *k8s_generic.Client[deployments.Resource] {
	return c.deployments
}

func (c *Client) Services() *k8s_generic.Client[services.Resource] {
	return c.services
}
