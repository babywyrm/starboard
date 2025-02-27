package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/aquasecurity/starboard/pkg/apis/aquasecurity/v1alpha1"
	"github.com/aquasecurity/starboard/pkg/operator/etc"
	"github.com/aquasecurity/starboard/pkg/operator/predicate"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type TTLReportReconciler struct {
	logr.Logger
	etc.Config
	client.Client
}

func (r *TTLReportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	installModePredicate, err := predicate.InstallModePredicate(r.Config)
	if err != nil {
		return err
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VulnerabilityReport{}, builder.WithPredicates(
			predicate.Not(predicate.IsBeingTerminated),
			installModePredicate)).
		Complete(r.reconcileReport())
	if err != nil {
		return err
	}
	return nil
}

func (r *TTLReportReconciler) reconcileReport() reconcile.Func {
	return func(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
		log := r.Logger.WithValues("report", req.NamespacedName)

		report := &v1alpha1.VulnerabilityReport{}
		err := r.Client.Get(ctx, req.NamespacedName, report)
		if err != nil {
			if errors.IsNotFound(err) {
				log.V(1).Info("Ignoring cached report that must have been deleted")
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("getting report from cache: %w", err)
		}

		ttlReportAnnotationStr, ok := report.Annotations[v1alpha1.TTLReportAnnotation]
		if !ok {
			log.V(1).Info("Ignoring report without TTL set")
			return ctrl.Result{}, nil
		}

		reportTTLTime, err := time.ParseDuration(ttlReportAnnotationStr)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed parsing %v with value %v %w", v1alpha1.TTLReportAnnotation, ttlReportAnnotationStr, err)
		}
		creationTime := report.Report.UpdateTimestamp
		ttlExpired, durationToTTLExpiration, err := ttlIsExpired(reportTTLTime, creationTime.Time)
		if err != nil {
			return ctrl.Result{}, err
		}
		if ttlExpired {
			log.V(1).Info("Removing vulnerabilityReport with expired TTL")
			err := r.Client.Delete(ctx, report, &client.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			// Since the report is deleted there is no reason to requeue
			return ctrl.Result{}, nil
		}
		log.V(1).Info("RequeueAfter", "durationToTTLExpiration", durationToTTLExpiration)
		return ctrl.Result{RequeueAfter: durationToTTLExpiration}, nil
	}
}

func ttlIsExpired(reportTTL time.Duration, creationTime time.Time) (bool, time.Duration, error) {
	expiresAt := creationTime.Add(reportTTL)
	currentTime := time.Now()
	isExpired := currentTime.After(expiresAt)

	if isExpired {
		return true, time.Duration(0), nil
	}

	expiresIn := expiresAt.Sub(currentTime)
	return false, expiresIn, nil
}
