package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/benjamin-wright/db-operator/pkg/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type migrationValidator struct{ claims *migrationClaims }

func (v *migrationValidator) ValidateCreate(ctx context.Context, pgms *v1alpha1.PostgresMigrationSet) (admission.Warnings, error) {
	request, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, err
	}
	dry := request.DryRun != nil && *request.DryRun
	_, err = v.claims.ensure(ctx, pgms, dry)
	return nil, err
}
func (v *migrationValidator) ValidateUpdate(ctx context.Context, old, next *v1alpha1.PostgresMigrationSet) (admission.Warnings, error) {
	if old.Spec.DatabaseRef != next.Spec.DatabaseRef || old.Spec.Database != next.Spec.Database {
		return nil, fmt.Errorf("migration database binding is immutable")
	}
	if !next.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return v.ValidateCreate(ctx, next)
}
func (v *migrationValidator) ValidateDelete(context.Context, *v1alpha1.PostgresMigrationSet) (admission.Warnings, error) {
	return nil, nil
}
func RegisterMigrationAdmission(mgr ctrl.Manager, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("migration claim namespace is required")
	}
	mgr.GetWebhookServer().Register("/validate-postgresmigrationset", admission.WithValidator(mgr.GetScheme(), &migrationValidator{claims: &migrationClaims{client: mgr.GetClient(), reader: mgr.GetAPIReader(), namespace: namespace}}))
	return nil
}
