package garbagecollector

import (
	"context"
	"fmt"
	"time"

	mustgatherv1alpha1 "github.com/masayag/must-gather-operator/pkg/apis/mustgather/v1alpha1"
	"github.com/masayag/must-gather-operator/pkg/controller/mustgatherreport"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// DefaultDeleteAfter is default time how long the must-gather data should be exposed.
const DefaultDeleteAfter = time.Minute * 5 // TODO: increase

var doneResult = reconcile.Result{} // no requeue
var rescheduleResult = reconcile.Result{RequeueAfter: time.Minute * 5}

var log = logf.Log.WithName("gc_mustgather")

// GC creates a new MustGather Garbage Collector (controller) and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func GC(mgr manager.Manager) error {
	return addGc(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMustGatherReport{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func addGc(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("mustgatherreport-controller-garbage-collector", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &mustgatherv1alpha1.MustGatherReport{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileMustGatherReport{}

// ReconcileMustGatherReport reconciles a MustGatherReport object
type ReconcileMustGatherReport struct {
	client client.Client
	scheme *runtime.Scheme
}

func (r *ReconcileMustGatherReport) pruneMustGatherReport(reqLogger logr.Logger, namespace string) reconcile.Result {
	result := doneResult

	opts := &client.ListOptions{
		Namespace: namespace,
	}

	mgrs := &mustgatherv1alpha1.MustGatherReportList{}
	err := r.client.List(context.TODO(), opts, mgrs)
	if err != nil {
		reqLogger.Error(err, "Failed to get list of MustGatherReport objects.")
		return rescheduleResult
	}

	log.Info(fmt.Sprintf("List of MustGatherReport objects retrieved, count: %d", len(mgrs.Items)))
	for _, obj := range mgrs.Items {
		/**
		deleteAfter := DefaultDeleteAfter
		if obj.Spec.DeleteAfter <= 0 {
			deleteAfter = obj.Spec.DeleteAfter
		}
		*/
		result = rescheduleResult
		timein := obj.ObjectMeta.CreationTimestamp.Time.Local().Add(DefaultDeleteAfter)
		timeNow := time.Now()

		if timeNow.After(timein) {
			err = r.client.Delete(context.TODO(), &obj)
			if err != nil {
				reqLogger.Error(err, fmt.Sprintf("Failed to remove MustGather object '%s' after time out, will be scheduled for next round.", obj.Name))
			}

			labelSelector := &client.ListOptions{LabelSelector: labels.SelectorFromSet(createByLabel(obj.Name))}

			// Delete route
			if err := r.deleteRoute(labelSelector); err != nil {
				reqLogger.Error(err, "Failed to delete route of the must-gather-report")
			}

			// Delete svc
			if err := r.deleteSvc(labelSelector); err != nil {
				reqLogger.Error(err, "Failed to delete route of the must-gather-report")
			}

			// Delete pod
			if err := r.deletePod(labelSelector); err != nil {
				reqLogger.Error(err, "Failed to delete route of the must-gather-report")
			}

			// Delete deploy
			if err := r.deleteDeploy(labelSelector); err != nil {
				reqLogger.Error(err, "Failed to delete route of the must-gather-report")
			}

			// Delete pvc
			if err := r.deletePvc(labelSelector); err != nil {
				reqLogger.Error(err, "Failed to delete route of the must-gather-report")
			}
		}
	}

	return result
}

func (r *ReconcileMustGatherReport) deleteRoute(labelSelector *client.ListOptions) error {
	routes := &routev1.RouteList{}
	if err := r.client.List(context.TODO(), labelSelector, routes); err != nil {
		return err
	}
	for _, route := range routes.Items {
		if err := r.client.Delete(context.TODO(), &route); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileMustGatherReport) deleteSvc(labelSelector *client.ListOptions) error {
	objs := &corev1.ServiceList{}
	if err := r.client.List(context.TODO(), labelSelector, objs); err != nil {
		return err
	}
	for _, obj := range objs.Items {
		if err := r.client.Delete(context.TODO(), &obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileMustGatherReport) deletePod(labelSelector *client.ListOptions) error {
	objs := &corev1.PodList{}
	if err := r.client.List(context.TODO(), labelSelector, objs); err != nil {
		return err
	}
	for _, obj := range objs.Items {
		if err := r.client.Delete(context.TODO(), &obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileMustGatherReport) deleteDeploy(labelSelector *client.ListOptions) error {
	objs := &appsv1.DeploymentList{}
	if err := r.client.List(context.TODO(), labelSelector, objs); err != nil {
		return err
	}
	for _, obj := range objs.Items {
		if err := r.client.Delete(context.TODO(), &obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileMustGatherReport) deletePvc(labelSelector *client.ListOptions) error {
	objs := &corev1.PersistentVolumeClaimList{}
	if err := r.client.List(context.TODO(), labelSelector, objs); err != nil {
		return err
	}
	for _, obj := range objs.Items {
		if err := r.client.Delete(context.TODO(), &obj); err != nil {
			return err
		}
	}
	return nil
}

func createByLabel(name string) map[string]string {
	return map[string]string{mustgatherreport.CreatedByLabel: name}
}

// Reconcile reads that state of the cluster for a MustGatherReport object and makes changes based on the state read
// and what is in the MustGatherReport.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileMustGatherReport) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("MustGather Garbage Collector")

	resultMustGather := r.pruneMustGatherReport(reqLogger, request.Namespace)

	return resultMustGather, nil // schedule potentially next GC round
}
