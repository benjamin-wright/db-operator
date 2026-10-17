package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func protocolClient(t *testing.T, objects ...client.Object) (client.Client, *runtime.Scheme) {
	t.Helper()
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(batchv1.AddToScheme(scheme)).To(Succeed())
	g.Expect(coordinationv1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1alpha1.PostgresMigrationSet{}).WithObjects(objects...).Build(), scheme
}
func protocolMigration() *v1alpha1.PostgresMigrationSet {
	return &v1alpha1.PostgresMigrationSet{ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "apps", UID: "uid-current", Generation: 1}, Spec: v1alpha1.PostgresMigrationSetSpec{DatabaseRef: "cluster", Database: "application", Artifact: "registry.example/migrations@sha256:0000000000000000000000000000000000000000000000000000000000000000", TargetRevision: 1}}
}

func TestMigrationClaimAtomicReservationAndDryRun(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	api, _ := protocolClient(t)
	claims := &migrationClaims{client: api, reader: api, namespace: "operator"}
	one := protocolMigration()
	two := one.DeepCopy()
	two.Name = "other"
	_, err := claims.ensure(ctx, one, true)
	g.Expect(err).NotTo(HaveOccurred())
	var leases coordinationv1.LeaseList
	g.Expect(api.List(ctx, &leases)).To(Succeed())
	g.Expect(leases.Items).To(BeEmpty())
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, object := range []*v1alpha1.PostgresMigrationSet{one, two} {
		wait.Add(1)
		go func(object *v1alpha1.PostgresMigrationSet) {
			defer wait.Done()
			_, err := claims.ensure(ctx, object, false)
			results <- err
		}(object)
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	g.Expect(successes).To(Equal(1))
	g.Expect(api.List(ctx, &leases)).To(Succeed())
	g.Expect(leases.Items).To(HaveLen(1))
	g.Expect(leases.Items[0].OwnerReferences).To(BeEmpty())
	owner := one
	if leases.Items[0].Annotations[migrationSetLabel] == two.Name {
		owner = two
	}
	_, err = claims.ensure(ctx, owner, false)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestMigrationJournalSurvivesRetentionAndMetadataChanges(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	ms := protocolMigration()
	ms.Generation = 7
	ms.Spec.Paused = true
	ms.Spec.JobTTL = &metav1.Duration{Duration: time.Nanosecond}
	ms.Annotations = map[string]string{migrationRetryAnnotation: "ack-only"}
	ms.Status.Operation = &v1alpha1.MigrationOperation{Generation: 1, Artifact: ms.Spec.Artifact, ResolvedArtifact: ms.Spec.Artifact, TargetRevision: 1, Phase: "Succeeded", JobRef: v1alpha1.MigrationJobReference{Name: "expired", UID: "old-job"}}
	api, scheme := protocolClient(t, ms)
	r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
	_, err := r.reconcileMigrationSet(ctx, ms)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ms.Status.Phase).To(Equal(v1alpha1.MigrationSetPhaseReady))
	g.Expect(ms.Status.Operation.JobRef.Name).To(Equal("expired"))
	g.Expect(ms.Status.LastHandledRetryAt).To(Equal("ack-only"))
	var jobs batchv1.JobList
	g.Expect(api.List(ctx, &jobs)).To(Succeed())
	g.Expect(jobs.Items).To(BeEmpty())
}

func TestMigrationJournalFencesMissingAndReplacedJobs(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "replacement"}[replacement], func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			ms := protocolMigration()
			ms.Status.Operation = &v1alpha1.MigrationOperation{Generation: 1, Artifact: ms.Spec.Artifact, ResolvedArtifact: ms.Spec.Artifact, TargetRevision: 1, Phase: "Running", JobRef: v1alpha1.MigrationJobReference{Name: "recorded", UID: "expected-uid"}}
			objects := []client.Object{ms}
			if replacement {
				objects = append(objects, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "recorded", Namespace: ms.Namespace, UID: "attacker"}})
			}
			api, scheme := protocolClient(t, objects...)
			r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
			_, err := r.reconcileMigrationSet(ctx, ms)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ms.Status.Operation.Phase).To(Equal("Unknown"))
			g.Expect(ms.Status.Phase).To(Equal(v1alpha1.MigrationSetPhaseFailed))
			_, err = r.reconcileMigrationSet(ctx, ms)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ms.Status.Operation.JobRef.UID).To(Equal("expected-uid"))
		})
	}
}

func TestFailedOperationRequiresChangedRetryAndPersistsBeforeProvisioning(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	ms := protocolMigration()
	ms.Status.LastHandledRetryAt = "first"
	ms.Annotations = map[string]string{migrationRetryAnnotation: "first"}
	ms.Status.Operation = &v1alpha1.MigrationOperation{Artifact: ms.Spec.Artifact, TargetRevision: 1, Phase: "Failed", JobRef: v1alpha1.MigrationJobReference{Name: "failed"}, Reason: "JobFailed", Message: "Failed"}
	api, scheme := protocolClient(t, ms)
	r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
	_, err := r.reconcileMigrationSet(ctx, ms)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ms.Status.Operation.JobRef.Name).To(Equal("failed"))
	ms.Annotations[migrationRetryAnnotation] = "explicit-recovery"
	_, err = r.reconcileMigrationSet(ctx, ms)
	g.Expect(err).To(HaveOccurred()) // Backend absent, but the durable reservation must survive.
	var persisted v1alpha1.PostgresMigrationSet
	g.Expect(api.Get(ctx, client.ObjectKeyFromObject(ms), &persisted)).To(Succeed())
	g.Expect(persisted.Status.Operation.Phase).To(Equal("Pending"))
	g.Expect(persisted.Status.Operation.JobRef.Name).NotTo(Equal("failed"))
	g.Expect(persisted.Status.LastHandledRetryAt).To(Equal("explicit-recovery"))
}

func pendingProtocolMigration() *v1alpha1.PostgresMigrationSet {
	ms := protocolMigration()
	ms.Status.Operation = &v1alpha1.MigrationOperation{Generation: 1, Artifact: ms.Spec.Artifact, ResolvedArtifact: ms.Spec.Artifact, TargetRevision: 1, Phase: "Pending", Reason: migrationAuthorityPending, JobRef: v1alpha1.MigrationJobReference{Name: "reserved"}}
	return ms
}

func TestPendingMigrationWithdrawalWithoutDatabase(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "superseded", true: "paused"}[paused], func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			ms := pendingProtocolMigration()
			ms.Spec.Paused = paused
			if !paused {
				ms.Spec.TargetRevision = 2
			}
			api, scheme := protocolClient(t, ms)
			g.Expect(api.Get(ctx, client.ObjectKeyFromObject(ms), ms)).To(Succeed())
			r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
			_, err := r.reconcileMigrationSet(ctx, ms)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ms.Status.Operation.Reason).To(Equal(migrationOperationWithdrawn))
			g.Expect(ms.Status.Operation.JobRef.Name).To(Equal("reserved"))
			g.Expect(ms.Status.Operation.Phase).To(Equal("Failed"))
			var jobs batchv1.JobList
			g.Expect(api.List(ctx, &jobs)).To(Succeed())
			g.Expect(jobs.Items).To(BeEmpty())
			// Resuming the latest desired request gets a new identity even if its
			// artifact and target match the request that was paused.
			ms.Spec.Paused = false
			g.Expect(api.Update(ctx, ms)).To(Succeed())
			_, err = r.reconcileMigrationSet(ctx, ms)
			g.Expect(err).To(HaveOccurred()) // No backend; only the new reservation is durable.
			g.Expect(api.Get(ctx, client.ObjectKeyFromObject(ms), ms)).To(Succeed())
			g.Expect(ms.Status.Operation.JobRef.Name).NotTo(Equal("reserved"))
			g.Expect(ms.Status.Operation.TargetRevision).To(Equal(ms.Spec.TargetRevision))
			g.Expect(ms.Status.Operation.Reason).To(Equal(migrationAuthorityPending))
		})
	}
}

func TestPendingMigrationWithdrawalLosesToArmingCAS(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	ms := pendingProtocolMigration()
	ms.Spec.Paused = true
	api, scheme := protocolClient(t, ms)
	g.Expect(api.Get(ctx, client.ObjectKeyFromObject(ms), ms)).To(Succeed())
	armed := ms.DeepCopy()
	armed.Status.Operation.Reason = migrationAuthorityArming
	g.Expect(api.Status().Update(ctx, armed)).To(Succeed())
	r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
	_, err := r.reconcileMigrationSet(ctx, ms)
	g.Expect(apierrors.IsConflict(err)).To(BeTrue())
	g.Expect(api.Get(ctx, client.ObjectKeyFromObject(ms), ms)).To(Succeed())
	g.Expect(ms.Status.Operation.Reason).To(Equal(migrationAuthorityArming))
	_, err = r.reconcileMigrationSet(ctx, ms)
	g.Expect(err).To(HaveOccurred()) // Unknown authority must be fenced, never guessed away.
	g.Expect(ms.Status.Operation.Phase).To(Equal("Pending"))
}

func TestPendingMigrationObservesUncachedJobBeforeWithdrawal(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "superseded", true: "paused"}[paused], func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			ms := pendingProtocolMigration()
			ms.Status.Operation.Reason = migrationAuthorityArming
			ms.Spec.Paused = paused
			if !paused {
				ms.Spec.TargetRevision = 2
			}
			controller := true
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "reserved", Namespace: ms.Namespace, UID: "running-job", Labels: map[string]string{migrationOperationLabel: "reserved"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: v1alpha1.GroupVersion.String(), Kind: "PostgresMigrationSet", Name: ms.Name, UID: ms.UID, Controller: &controller}}}}
			api, scheme := protocolClient(t, ms, job)
			r := &PostgresMigrationSetReconciler{MigrationImage: "image", client: postgresMigrationSetClient{inner: api, scheme: scheme}, claims: &migrationClaims{client: api, reader: api, namespace: "operator"}}
			claim, err := r.claims.ensure(ctx, ms, false)
			g.Expect(err).NotTo(HaveOccurred())
			_, err = r.observeOperation(ctx, ms, claim, &batchv1.JobList{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ms.Status.Operation.JobRef.UID).To(Equal("running-job"))
			g.Expect(ms.Status.Operation.Phase).To(Equal("Running"))
		})
	}
}

func TestMigrationLaunchRequiresCurrentLiveAuthority(t *testing.T) {
	cases := map[string]func(*v1alpha1.PostgresMigrationSet){
		"paused":        func(ms *v1alpha1.PostgresMigrationSet) { ms.Spec.Paused = true },
		"target":        func(ms *v1alpha1.PostgresMigrationSet) { ms.Spec.TargetRevision = 2 },
		"artifact":      func(ms *v1alpha1.PostgresMigrationSet) { ms.Spec.Artifact = "other" },
		"generation":    func(ms *v1alpha1.PostgresMigrationSet) { ms.Generation++ },
		"withdrawn":     func(ms *v1alpha1.PostgresMigrationSet) { ms.Status.Operation.Phase = "Failed" },
		"new operation": func(ms *v1alpha1.PostgresMigrationSet) { ms.Status.Operation.JobRef.Name = "new" },
		"recorded job":  func(ms *v1alpha1.PostgresMigrationSet) { ms.Status.Operation.JobRef.UID = "recorded" },
		"owner":         func(ms *v1alpha1.PostgresMigrationSet) { ms.UID = "replacement" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			expected := pendingProtocolMigration()
			live := expected.DeepCopy()
			g.Expect(validateMigrationLaunch(expected, live)).To(Succeed())
			change(live)
			g.Expect(validateMigrationLaunch(expected, live)).NotTo(Succeed())
		})
	}
}
