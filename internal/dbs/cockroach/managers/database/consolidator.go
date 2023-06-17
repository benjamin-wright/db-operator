package database

type consolidator struct {
	resources map[string]map[string]struct{}
}

func newConsolidator() *consolidator {
	return &consolidator{
		resources: map[string]map[string]struct{}{},
	}
}

func (c *consolidator) add(name string, namespace string) {
	if _, ok := c.resources[namespace]; !ok {
		c.resources[namespace] = map[string]struct{}{}
	}

	c.resources[namespace][name] = struct{}{}
}

func (c *consolidator) getNamespaces() []string {
	namespaces := []string{}

	for namespace := range c.resources {
		namespaces = append(namespaces, namespace)
	}

	return namespaces
}

func (c *consolidator) getNames(namespace string) []string {
	names := []string{}

	for name := range c.resources[namespace] {
		names = append(names, name)
	}

	return names
}
