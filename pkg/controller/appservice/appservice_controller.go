package appservice

import (
	"context"
	"fmt"
	v1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	appv1alpha1 "github.com/sparkoo/monitoring-operator-prototype/pkg/apis/app/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_appservice")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new AppService Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileAppService{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("appservice-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource AppService
	err = c.Watch(&source.Kind{Type: &appv1alpha1.AppService{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner AppService
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &appv1alpha1.AppService{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileAppService implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileAppService{}

// ReconcileAppService reconciles a AppService object
type ReconcileAppService struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a AppService object and makes changes based on the state read
// and what is in the AppService.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileAppService) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling AppService")

	// Fetch the AppService instance
	instance := &appv1alpha1.AppService{}
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

	// Define a new Pod object
	pod := newPodForCR(instance)

	// Set AppService instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, pod, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Pod already exists
	found := &corev1.Pod{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{}, err
		}

		// Pod created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if err := deployService(r, instance); err != nil {
		return reconcile.Result{}, err
	}

	if err := ensureOperatorGroup(r, instance); err != nil {
		return reconcile.Result{}, err
	}
	if err := installPrometheus(r, instance); err != nil {
		return reconcile.Result{}, err
	}

	if err := setupPrometheus(r, instance); err != nil {
		return reconcile.Result{}, err
	}

	// Pod already exists - don't requeue
	reqLogger.Info("Skip reconcile: Pod already exists", "Pod.Namespace", found.Namespace, "Pod.Name", found.Name)
	return reconcile.Result{}, nil
}

func deployService(r *ReconcileAppService, app *appv1alpha1.AppService) error {
	serviceName := app.Name + "-appservice"
	findErr := r.client.Get(context.TODO(), types.NamespacedName{Name: serviceName, Namespace: app.Namespace}, &corev1.Service{})
	if findErr == nil {
		log.Info("Service found. Nothing to do.")
	} else if errors.IsNotFound(findErr) {
		log.Info("Create service")
		appService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: app.Namespace,
				Labels:    map[string]string{"app": app.Name},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Port:       app.Spec.Port,
						TargetPort: intstr.IntOrString{IntVal: app.Spec.Port, Type: intstr.Int},
						Name:       "data-port",
					},
				},
				Selector: map[string]string{"app": app.Name},
			},
		}
		return r.client.Create(context.TODO(), appService)
	} else {
		return findErr
	}

	return nil
}

func setupPrometheus(r *ReconcileAppService, app *appv1alpha1.AppService) error {
	log.Info("Create ServiceMonitor object")

	serviceMonitorName := app.Name + "-servicemonitor"
	foundServiceMonitor := &v1.ServiceMonitor{}
	findErr := r.client.Get(context.TODO(), types.NamespacedName{Name: serviceMonitorName, Namespace: app.Namespace}, foundServiceMonitor)
	if findErr == nil {
		log.Info(fmt.Sprintf("ServiceMonitor [%s] found, nothing to do", serviceMonitorName))
	} else if errors.IsNotFound(findErr) {
		log.Info(fmt.Sprintf("ServiceMonitor [%s] not found, creating ...", serviceMonitorName))
		newServiceMonitor := &v1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceMonitorName,
				Namespace: app.Namespace,
			},
			Spec: v1.ServiceMonitorSpec{
				Endpoints: []v1.Endpoint{
					{Port: "data-port", Interval: "1s"},
				},
				Selector: metav1.LabelSelector{
					MatchLabels: map[string]string{"app": app.Name},
				},
				NamespaceSelector: v1.NamespaceSelector{
					MatchNames: []string{app.Namespace},
				},
			},
		}
		if err := r.client.Create(context.TODO(), newServiceMonitor); err != nil {
			return err
		}
	} else {
		log.Error(findErr, "")
		return findErr
	}
	return nil
}

func ensureOperatorGroup(r *ReconcileAppService, app *appv1alpha1.AppService) error {
	log.Info("Ensure to have OperatorGroup")

	foundGroup := &operatorsv1.OperatorGroup{}
	findErr := r.client.Get(context.TODO(), types.NamespacedName{Name: app.Name + "-og", Namespace: app.Namespace}, foundGroup)
	if findErr == nil {
		log.Info("OperatorGroup found, don't need to create it")
	} else if errors.IsNotFound(findErr) {
		newOperatorGroup := &operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name + "-og",
				Namespace: app.Namespace,
			},
			Spec: operatorsv1.OperatorGroupSpec{TargetNamespaces: []string{app.Namespace}},
		}
		if createErr := r.client.Create(context.TODO(), newOperatorGroup); createErr != nil {
			return fmt.Errorf("failed to create OperatorGroup")
		}
		if err := controllerutil.SetControllerReference(app, newOperatorGroup, r.scheme); err != nil {
			return err
		}
		log.Info("OperatorGroup created")
	} else {
		log.Error(findErr, "")
		return findErr
	}
	return nil
}

func installPrometheus(r *ReconcileAppService, app *appv1alpha1.AppService) error {
	installOperator("prometheus-operator")
	//subscription := &operators.Subscription{}
	//if err := r.client.Create(context.TODO(), subscription); err != nil {
	//	return err
	//}
	//findErr := r.client.Get(context.TODO(), types.NamespacedName{Name: app.Name + "-promSubs", Namespace: app.Namespace}, foundPromOp)
	//if findErr == nil {
	//	log.Info("Prometheus Operator Subscription found, nothing to do")
	//} else if errors.IsNotFound(findErr) {
	//	log.Info("Create Operator Subscription here !!!")
	//} else {
	//	log.Info("eeer")
	//	log.Error(findErr, "")
	//	return findErr
	//}

	foundPrometheus := &v1.Prometheus{}
	findErr := r.client.Get(context.TODO(), types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, foundPrometheus)
	if findErr == nil {
		log.Info("Prometheus instance already exists")
	} else if errors.IsNotFound(findErr) {
		log.Info("Need to create new Prometheus instance")
		prom := &v1.Prometheus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: app.Namespace,
			},
			Spec: v1.PrometheusSpec{
				ServiceMonitorSelector: &metav1.LabelSelector{},
				ServiceMonitorNamespaceSelector: &metav1.LabelSelector{},
				ServiceAccountName:              "prometheus-k8s",
			},
		}
		return r.client.Create(context.TODO(), prom)
	} else {
		return findErr
	}

	return nil
}

func installOperator(name string) {
	log.Info(fmt.Sprintf("install [%s]", name))
	log.Info("installing operator with Subscription objects does not work for some reason :/")
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *appv1alpha1.AppService) *corev1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: cr.Spec.Image,
				},
			},
		},
	}
}
