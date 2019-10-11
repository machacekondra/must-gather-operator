package mustgatherreport

import (
	"context"
	"fmt"
	"path"
	"sync"
	"time"

	mustgatherv1alpha1 "github.com/masayag/must-gather-operator/pkg/apis/mustgather/v1alpha1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/library-go/pkg/operator/resource/retry"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errorsutils "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_mustgatherreport")

const (
	// SourceDir points to folder on PV to write and copy files from for gather data
	SourceDir = "/must-gather/"

	// CreatedByLabel label is used as a marker for pods that are owned by MustGatherReport CR
	CreatedByLabel = "must-gather/created-by"
)

// Add creates a new MustGatherReport Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMustGatherReport{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("mustgatherreport-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource MustGatherReport
	err = c.Watch(&source.Kind{Type: &mustgatherv1alpha1.MustGatherReport{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner MustGatherReport
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &mustgatherv1alpha1.MustGatherReport{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileMustGatherReport implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileMustGatherReport{}

// ReconcileMustGatherReport reconciles a MustGatherReport object
type ReconcileMustGatherReport struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
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
	reqLogger.Info("Reconciling MustGatherReport")

	// Fetch the MustGatherReport instance
	instance := &mustgatherv1alpha1.MustGatherReport{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if ok, err := r.IsValid(instance); !ok {
		return reconcile.Result{}, err
	}

	if ok := r.IsCompleted(instance); ok {
		reqLogger.Info("Skip reconcile: must-gather report is already created", "MustGatherReport.Namespace", instance.Namespace,
			"MustGatherReport.Name", instance.Name, "MustGatherReport.Status.ReportURL", instance.Status.ReportURL)
		return reconcile.Result{}, nil
	}

	podList := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(labelsForMustGather(instance.Name))
	listOps := &client.ListOptions{
		Namespace:     instance.Namespace,
		LabelSelector: labelSelector,
	}
	err = r.client.List(context.TODO(), listOps, podList)
	if err != nil {
		reqLogger.Error(err, "Failed to list pods.", "MustGatherReport.Namespace", instance.Namespace, "MustGatherReport.Name", instance.Name)
		return reconcile.Result{}, err
	}

	if len(podList.Items) == 0 {
		// Create and run pods for gathering diagnostic data
		pvcName, err := r.runMustGather(instance)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Expose the must gather data:
		url, err := r.exposeMustGatherData(instance, pvcName)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Update status.ReportURL if needed
		instance.Status.ReportURL = url
		err = r.client.Status().Update(context.TODO(), instance)
		if err != nil {
			reqLogger.Error(err, "Failed to update MustGather operator status")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// exposeMustGatherData Exposes the nginx with oauth proxy containg compressed must-gather data and return the URL of the nginx:
func (r *ReconcileMustGatherReport) exposeMustGatherData(cr *mustgatherv1alpha1.MustGatherReport, pvcName string) (string, error) {
	// Create service serving the compressed/filtered must-gather data:
	svc := r.newService(cr)
	if err := r.client.Create(context.TODO(), svc); err != nil {
		log.Error(err, "Failed to create service")
	}

	// Create route serving the compressed/filtered must-gather data:
	route := r.newRoute(cr, svc)
	if err := r.client.Create(context.TODO(), route); err != nil {
		log.Error(err, "Failed to create route")
	}

	// Patch SA:
	if err := r.patchServiceAccount(cr, route); err != nil {
		log.Error(err, "Failed to path service account")
	}

	// Deploy the ngnix & oauth proxy serving the must gather data:
	nginx := r.newNginx(cr, pvcName)
	if err := r.client.Create(context.TODO(), nginx); err != nil {
		log.Error(err, "Failed to create nginx hosting the must gather data")
	}

	// Wait for route ingress host:
	for {
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, route); err != nil {
			log.Error(err, "Failed to fetch route")
		}
		//log.Info("Route:", route.Status)
		if len(route.Status.Ingress) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return route.Status.Ingress[0].Host, nil // FIXME which ingress index to access?
}

func (r *ReconcileMustGatherReport) patchServiceAccount(cr *mustgatherv1alpha1.MustGatherReport, route *routev1.Route) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "must-gather-operator",
			Namespace: cr.Namespace,
			Annotations: map[string]string{
				"serviceaccounts.openshift.io/oauth-redirectreference.primary": "{\"kind\":\"OAuthRedirectReference\",\"apiVersion\":\"v1\",\"reference\":{\"kind\":\"Route\",\"name\":\"" + route.Name + "\"}}",
			},
		},
	}
	return r.client.Update(context.TODO(), sa)
}

func (r *ReconcileMustGatherReport) newNginx(cr *mustgatherv1alpha1.MustGatherReport, pvcName string) *appsv1.Deployment {
	const nginx = "must-gather-operator-nginx"
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: nginx + "-",
			Namespace:    cr.Namespace,
			Labels:       labelsForMustGather(cr.Name),
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": nginx,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": nginx,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "must-gather-operator",
					Containers: []corev1.Container{
						// Oauth-proxy
						{
							Name:            "oauth-proxy",
							Image:           "openshift/oauth-proxy:latest",
							ImagePullPolicy: "Always",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8443,
									Name:          "public",
								},
							},
							Args: []string{
								"--https-address=:8443",
								"--provider=openshift",
								"--openshift-service-account=must-gather-operator",
								"--upstream=http://localhost:8080",
								"--tls-cert=/etc/tls/private/tls.crt",
								"--tls-key=/etc/tls/private/tls.key",
								"--cookie-secret=SECRET",
								"--request-logging=true", // REMOVE ME
								// # Allows access if the user can view the service 'kubelet' in namespace 'kube-system' (administrators only)
								"--openshift-sar={\"namespace\":\"kube-system\",\"resource\":\"services\",\"name\":\"kubelet\",\"verb\":\"get\"}",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/etc/tls/private",
									Name:      "must-gather-operator-tls",
								},
							},
						},
						// Nginx
						{
							Name:  "nginx",
							Image: "bitnami/nginx",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/opt/bitnami/nginx/html",
									Name:      pvcName,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "must-gather-operator-tls",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "must-gather-operator-tls",
								},
							},
						},
						{
							Name: pvcName,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *ReconcileMustGatherReport) newService(cr *mustgatherv1alpha1.MustGatherReport) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "must-gather-operator-",
			Namespace:    cr.Namespace,
			Labels:       labelsForMustGather(cr.Name),
			Annotations: map[string]string{
				"service.alpha.openshift.io/serving-cert-secret-name": "must-gather-operator-tls",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "must-gather-operator",
					Port:       443,
					TargetPort: intstr.FromInt(8443),
				},
			},
			Selector: map[string]string{
				"app": "must-gather-operator-nginx",
			},
		},
	}
}

func (r *ReconcileMustGatherReport) newRoute(cr *mustgatherv1alpha1.MustGatherReport, svc *corev1.Service) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "must-gather-operator-",
			Namespace:    cr.Namespace,
			Labels:       labelsForMustGather(cr.Name),
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: svc.Name,
			},
			TLS: &routev1.TLSConfig{
				Termination: "reencrypt",
			},
		},
	}
}

// IsValid checks the validity of the MustGatherReport cr
func (r *ReconcileMustGatherReport) IsValid(obj metav1.Object) (bool, error) {
	cr, ok := obj.(*mustgatherv1alpha1.MustGatherReport)
	if !ok {
		return false, fmt.Errorf("not a MustGatherReport object")
	}

	if len(cr.Spec.Images) == 0 {
		return false, fmt.Errorf("missing an image")
	}

	return true, nil
}

// IsCompleted checks if the report was created and available for download
func (r *ReconcileMustGatherReport) IsCompleted(cr *mustgatherv1alpha1.MustGatherReport) bool {
	return len(cr.Status.ReportURL) > 0
}

// Run creates and runs a must-gather pod.d
func (r *ReconcileMustGatherReport) runMustGather(cr *mustgatherv1alpha1.MustGatherReport) (string, error) {
	var err error
	log := log.WithValues("MustGatherReport.Namespace", cr.Namespace, "MustGatherReport.Name", cr.Name)

	// create pods
	var pods []*corev1.Pod
	var pod *corev1.Pod
	var pvc *corev1.PersistentVolumeClaim

	// TODO: if there is only one storage-class which isn't set as default, use it
	defaultStorageClass := r.getDefaultStorageClass()
	if defaultStorageClass == "" {
		log.Error(err, "Failed to create pvc, no default storage class defined")
		return "", err
	}

	for _, image := range cr.Spec.Images {
		pvc = r.newPVC(cr, cr.Namespace, &defaultStorageClass) // FIXME: works only for single PVC
		if err := r.client.Create(context.TODO(), pvc); err != nil {
			log.Error(err, "Failed to create pvc")
		}
		if err := controllerutil.SetControllerReference(cr, pvc, r.scheme); err != nil {
			return "", err
		}

		pod = r.newPod(image, cr, cr.Namespace, pvc)
		err := r.client.Create(context.TODO(), pod)
		if err != nil {
			return "", err
		}

		// Set MustGatherReport instance as the owner and controller
		err = controllerutil.SetControllerReference(cr, pod, r.scheme)
		if err != nil {
			return "", err
		}

		log.Info("pod for plug-in Image created", "Image", image)
		pods = append(pods, pod)
	}

	var wg sync.WaitGroup
	wg.Add(len(pods))
	errs := make(chan error, len(pods))
	for _, pod := range pods {
		go func(pod *corev1.Pod) {
			defer wg.Done()

			// wait for gather container to be running (gather is running)
			if err := r.waitForGatherContainerRunning(pod); err != nil {
				log.Info("gather did not start: Message", "Message", err)
				errs <- fmt.Errorf("gather did not start for pod %s: %s", pod.Name, err)
				return
			}

			// wait for pod to be running (gather has completed)
			log.Info("waiting for gather to complete")
			if err := r.waitForPodRunning(pod); err != nil {
				log.Error(err, "gather never finished")
				errs <- fmt.Errorf("gather never finished for pod %s: %s", pod.Name, err)
				return
			}
		}(pod)
	}
	wg.Wait()
	close(errs)
	var arr []error
	for i := range errs {
		arr = append(arr, i)
	}
	errors := errorsutils.NewAggregate(arr)
	log.Info("Gather for all images finished: Message", "Message", errors)
	return pvc.Name, errors
}

func (r *ReconcileMustGatherReport) waitForPodRunning(pod *corev1.Pod) error {
	phase := pod.Status.Phase
	gatherPod := &corev1.Pod{}
	err := wait.PollImmediate(time.Second, 10*time.Minute, func() (bool, error) {
		var err error
		if err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, gatherPod); err != nil {
			return false, err
		}
		phase = gatherPod.Status.Phase
		return phase != corev1.PodPending, nil
	})
	if err != nil {
		return err
	}
	if phase != corev1.PodRunning {
		return fmt.Errorf("pod is not running: %v", phase)
	}
	return nil
}

func (r *ReconcileMustGatherReport) waitForGatherContainerRunning(pod *corev1.Pod) error {
	gatherPod := &corev1.Pod{}
	return wait.PollImmediate(time.Second, 10*time.Minute, func() (bool, error) {
		var err error
		if err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, gatherPod); err == nil {
			if len(gatherPod.Status.InitContainerStatuses) == 0 {
				return false, nil
			}
			state := gatherPod.Status.InitContainerStatuses[0].State
			if state.Waiting != nil && state.Waiting.Reason == "ErrImagePull" {
				return true, fmt.Errorf("unable to pull image: %v: %v", state.Waiting.Reason, state.Waiting.Message)
			}
			running := state.Running != nil
			terminated := state.Terminated != nil
			return running || terminated, nil
		}
		if retry.IsHTTPClientError(err) {
			return false, nil
		}
		return false, err
	})
}

func labelsForMustGather(name string) map[string]string {
	return map[string]string{"app": "must-gather", CreatedByLabel: name}
}

func (r *ReconcileMustGatherReport) getDefaultStorageClass() string {
	storageClassList := &storagev1.StorageClassList{}
	err := r.client.List(context.TODO(), &client.ListOptions{}, storageClassList)
	if err != nil {
		return ""
	}

	for _, sc := range storageClassList.Items {
		if sc.GetAnnotations()["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sc.Name
		}
	}

	return ""
}

func (r *ReconcileMustGatherReport) newPVC(cr *mustgatherv1alpha1.MustGatherReport, nsName string, storageClass *string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "must-gather-pvc-",
			Namespace:    nsName,
			Labels:       labelsForMustGather(cr.Name),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				},
			},
			StorageClassName: storageClass,
		},
	}
}

// newPod creates a pod with 2 containers with a shared volume mount:
// - gather: init containers that run gather command
// - copy: no-op container we can exec into
func (r *ReconcileMustGatherReport) newPod(image string, cr *mustgatherv1alpha1.MustGatherReport, nsName string, pvc *corev1.PersistentVolumeClaim) *corev1.Pod {
	zero := int64(0)
	ret := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "must-gather-",
			Labels:       labelsForMustGather(cr.Name),
			Namespace:    nsName,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "must-gather-output",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvc.Name,
							ReadOnly:  false,
						},
					},
				},
			},
			InitContainers: []corev1.Container{
				{
					Name:    "gather",
					Image:   image,
					Command: []string{"/usr/bin/gather"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "must-gather-output",
							MountPath: path.Clean(SourceDir),
							ReadOnly:  false,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "copy",
					Image:   image,
					Command: []string{"/bin/bash", "-c", "trap : TERM INT; sleep infinity & wait"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "must-gather-output",
							MountPath: path.Clean(SourceDir),
							ReadOnly:  false,
						},
					},
				},
			},
			TerminationGracePeriodSeconds: &zero,
			Tolerations: []corev1.Toleration{
				{
					Operator: "Exists",
				},
			},
		},
	}
	return ret
}
