package dag

// import (
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clients"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/k8s/clusters"
// 	"github.com/benjamin-wright/db-operator/v2/internal/dbs/postgres/managers/dag/model"
// 	"github.com/benjamin-wright/db-operator/v2/internal/state/bucket"
// )

// type Resolver struct {
// 	clusters bucket.Bucket[*model.Cluster]
// }

// func New() Resolver {
// 	return Resolver{
// 		clusters: bucket.NewBucket[*model.Cluster](),
// 	}
// }

// func (r Resolver) AddCluster(c clusters.Resource) {
// 	r.clusters.Add(model.NewCluster(c))
// }

// func (r Resolver) AddClient(u clients.Resource) {
// 	cluster, ok := r.clusters.Get(u.GetClusterID())
// 	if !ok {
// 		return
// 	}

// 	cluster.addClient(u)

// 	return

// }
