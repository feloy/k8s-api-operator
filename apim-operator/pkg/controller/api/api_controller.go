package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"github.com/cbroglie/mustache"
	wso2v1alpha1 "github.com/wso2/k8s-apim-operator/apim-operator/pkg/apis/wso2/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strings"

	"bytes"
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

var log = logf.Log.WithName("controller_api")

//XMGWProductionEndpoints represents the structure of endpoint
type XMGWProductionEndpoints struct {
	Urls []string `yaml:"urls"`
}

// Add creates a new API Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileAPI{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("api-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource API
	err = c.Watch(&source.Kind{Type: &wso2v1alpha1.API{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner API
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &wso2v1alpha1.API{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileAPI{}

// ReconcileAPI reconciles a API object
type ReconcileAPI struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a API object and makes changes based on the state read
// and what is in the API.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileAPI) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling API")

	// Fetch the API instance
	instance := &wso2v1alpha1.API{}
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

	//Check if the configmap mentioned in crd object exist
	apiConfigMapRef := instance.Spec.Definition.ConfigMapKeyRef.Name
	log.Info(apiConfigMapRef)

	apiConfigMap := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: apiConfigMapRef, Namespace: "default"}, apiConfigMap)

	if err != nil && errors.IsNotFound(err) {
		log.Error(err, "Specified configmap is not found: %s", apiConfigMapRef)
		return reconcile.Result{}, err
	} else if err != nil {
		log.Error(err, "error ")
		return reconcile.Result{}, err
	}

	//Fetch swagger data from configmap
	swaggerDataMap := apiConfigMap.Data
	var swaggerData string
	var swaggerDataFile string
	var imageName string
	for key, value := range swaggerDataMap {
		swaggerData = value
		swaggerDataFile = key
	}
	fmt.Println("swagger data file : ", swaggerDataFile)

	swagger, err := openapi3.NewSwaggerLoader().LoadSwaggerFromData([]byte(swaggerData))
	if err != nil {
		log.Error(err, "Swagger loading error ")
	}

	imageName =  strings.ReplaceAll(swagger.Info.Title, " ", "") + ":" + swagger.Info.Version

	//Get endpoint from swagger and replace it with targetendpoint kind service endpoint

	//api level endpoint
	data, ok := swagger.Extensions["x-mgw-production-endpoints"]
	if ok {
		prodEp := XMGWProductionEndpoints{}
		var endPoint string
		datax, ok1 := data.(json.RawMessage)

		if ok1 {
			err = json.Unmarshal(datax, &endPoint)
			if err == nil {
				//check if service is available
				currentService := &corev1.Service{}
				err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "default",
					Name: endPoint}, currentService)

				if err != nil && errors.IsNotFound(err) {
					log.Error(err, "Service CRD object is not found")
				} else if err != nil {
					log.Error(err, "Error in getting service")
				} else {
					endPoint = "https://" + endPoint
					checkt := []string{endPoint}
					prodEp.Urls = checkt
					swagger.Extensions["x-mgw-production-endpoints"] = prodEp
				}
			}
		}
	}

	//resource level endpoint
	for url, p := range swagger.Paths {
		fmt.Println(url)
		data1, c1 := p.Get.Extensions["x-mgw-production-endpoints"]
		if c1 {
			prodEp := XMGWProductionEndpoints{}
			var endPoint string
			datax, ok1 := data1.(json.RawMessage)
			if ok1 {
				err = json.Unmarshal(datax, &endPoint)
				if err == nil {
					//check if service is available
					currentService := &corev1.Service{}
					err = r.client.Get(context.TODO(), types.NamespacedName{Namespace: "default",
						Name: endPoint}, currentService)

					if err != nil && errors.IsNotFound(err) {
						log.Error(err, "Service CRD object is not found")
					} else if err != nil {
						log.Error(err, "Error in getting service")
					} else {
						endPoint = "https://" + endPoint
						checkt := []string{endPoint}
						prodEp.Urls = checkt
						p.Get.Extensions["x-mgw-production-endpoints"] = prodEp
					}
				}
			}
		}
	}

	//reformatting swagger
	final, err := swagger.MarshalJSON()
	var prettyJSON bytes.Buffer
	errIndent := json.Indent(&prettyJSON, final, "", "  ")
	if errIndent != nil {
		log.Error(errIndent, "Error in pretty json")
	}

	newSwagger := string(prettyJSON.Bytes())
	fmt.Println(newSwagger)

	//update configmap with modified swagger

	swaggerConfMap, err := createConfigMap(apiConfigMapRef, swaggerDataFile, newSwagger)
	if err != nil {
		log.Error(err, "Error in modified swagger configmap structure")
	}

	log.Info("Updating swagger configmap")
	errConf := r.client.Update(context.TODO(), swaggerConfMap)
	if errConf != nil {
		log.Error(err, "Error in modified swagger configmap update")
	}

	// gets the data from analytics secret
	analyticsData, err := getSecretData(r)

	//writes into the conf file

	if err == nil && analyticsData != nil && analyticsData["username"] != nil &&
		analyticsData["password"] != nil {
		analyticsEnabled = "true"
		analyticsUsername = string(analyticsData["username"])
		analyticsPassword = string(analyticsData["password"])
	}

	reqLogger.Info("getting security instance")

	//get security instance. sample secret name is hard coded for now.
	security := &wso2v1alpha1.Security{}
	errGetSec := r.client.Get(context.TODO(), types.NamespacedName{Name: "example-security-test-oauth", Namespace: "wso2-system"}, security)

	if errGetSec != nil && errors.IsNotFound(errGetSec) {
		reqLogger.Info("defined security instance is not found")
		return reconcile.Result{}, errGetSec
	}

	//get certificate
	certificateSecret := &corev1.Secret{}
	errc := r.client.Get(context.TODO(), types.NamespacedName{Name: security.Spec.Certificate, Namespace: "wso2-system"}, certificateSecret)

	if errc != nil && errors.IsNotFound(errc) {
		reqLogger.Info("defined cretificate is not found")
		return reconcile.Result{}, errc
	}

	if security.Spec.Type == "Oauth" {

		//fetch credentials from the secret created
		errGetCredentials := getCredentials(r, security.Spec.Credentials)

		if errGetCredentials != nil {
			log.Error(errGetCredentials, "Error occured when retriving credentials")
		} else {
			log.Info("Credentials successfully retrived")
		}
	}

	filename := "/usr/local/bin/microgwconf.mustache"
	output, err := mustache.RenderFile(filename, map[string]string{
		"keystorePath":                   keystorePath,
		"keystorePassword":               keystorePassword,
		"truststorePath":                 truststorePath,
		"truststorePassword":             truststorePassword,
		"keymanagerServerurl":            keymanagerServerurl,
		"keymanagerUsername":             keymanagerUsername,
		"keymanagerPassword":             keymanagerPassword,
		"issuer":                         issuer,
		"audience":                       audience,
		"certificateAlias":               certificateAlias,
		"enabledGlobalTMEventPublishing": enabledGlobalTMEventPublishing,
		"basicUsername":                  basicUsername,
		"basicPassword":                  basicPassword,
		"analyticsEnabled":               analyticsEnabled,
		"analyticsUsername":              analyticsUsername,
		"analyticsPassword":              analyticsPassword})

	if err != nil {
		log.Error(err, "error in rendering ")
	}

	//writes the created conf file to secret
	errCreateSecret := createMGWSecret(r, output)
	if errCreateSecret != nil {
		log.Error(errCreateSecret, "Error in creating conf secret")
	} else {
		log.Info("Successfully created secret")
	}

	//todo: make a deployment
	pod := generateMgwImage(instance, imageName)

	// Set API instance as the owner and controller
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
	dep := createMgwDeployment(instance, imageName);
	reqLogger.Info("Dep", dep.Name)
	// Pod already exists - don't requeue
	reqLogger.Info("Skip reconcile: Pod already exists", "Pod.Namespace", found.Namespace, "Pod.Name", found.Name)
	return reconcile.Result{}, nil
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *wso2v1alpha1.API) *corev1.Pod {
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
					Name:    "busybox",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

// gets the data from analytics secret
func getSecretData(r *ReconcileAPI) (map[string][]byte, error) {
	var analyticsData map[string][]byte
	// Check if this secret exists
	analyticsSecret := &corev1.Secret{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: "analytics-secret", Namespace: "wso2-system"}, analyticsSecret)

	if err != nil && errors.IsNotFound(err) {
		log.Error(err, "Analytics Secret is not found")
		return analyticsData, err

	} else if err != nil {
		log.Error(err, "error ")
		return analyticsData, err

	}

	analyticsData = analyticsSecret.Data
	log.Info("Analytics Secret exists")
	fmt.Println("DATA")
	fmt.Println(string(analyticsData["username"]))
	fmt.Println(string(analyticsData["password"]))
	fmt.Println("END")
	return analyticsData, nil

}

func createMGWSecret(r *ReconcileAPI, confData string) error {
	var apimSecret *corev1.Secret

	apimSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mgw-secret",
			Namespace: "wso2-system",
		},
	}

	apimSecret.Data = map[string][]byte{
		"confData": []byte(confData),
	}

	// Check if this secret exists
	checkSecret := &corev1.Secret{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: "mgw-secret", Namespace: "wso2-system"}, checkSecret)

	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating secret ")
		errSecret := r.client.Create(context.TODO(), apimSecret)
		return errSecret
	} else if err != nil {
		log.Error(err, "error ")
		return err
	} else {
		log.Info("Updating secret")
		errSecret := r.client.Update(context.TODO(), apimSecret)
		return errSecret
	}

}

// createConfigMap creates a config file with the swagger
func createConfigMap(apiConfigMapRef string, swaggerDataFile string, newSwagger string) (*corev1.ConfigMap, error) {

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      apiConfigMapRef,
			Namespace: "default",
		},
		Data: map[string]string{
			swaggerDataFile: newSwagger,
		},
	}, nil
}

func getCredentials(r *ReconcileAPI, name string) error {

	hasher := sha1.New()

	//get the secret included credentials
	credentialSecret := &corev1.Secret{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: "wso2-system"}, credentialSecret)

	if err != nil && errors.IsNotFound(err) {
		fmt.Println("secret not found")
		return err
	}

	//get the username and the password
	for k, v := range credentialSecret.Data {
		if strings.EqualFold(k, "username") {
			basicUsername = string(v)
			fmt.Println("basic username")
			fmt.Println(basicUsername)
		}
		if strings.EqualFold(k, "password") {

			//encode password to sha1
			_, err := hasher.Write([]byte(v))
			if err != nil {
				return err
			}

			//convert encoded password to a hex string
			basicPassword = hex.EncodeToString(hasher.Sum(nil))

			fmt.Printf("%x\n", hasher.Sum(nil))

		}
	}
	return nil
}

//Generate micro-gateway image and push into the registry
func generateMgwImage(cr *wso2v1alpha1.API, imageName string) *corev1.Pod {

	labels := map[string]string{
		"app": cr.Name,
	}
	apiConfMap := cr.Spec.Definition.ConfigMapKeyRef.Name;
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "gen-container" + cr.Name,
					Image: "gcr.io/kaniko-project/executor:latest",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      swaggerVolume,
							MountPath: swaggerLocation,
							ReadOnly:  true,
						},
						{
							Name:      mgwDockerFile,
							MountPath: dockerFileLocation,
						},
						{
							Name:      dockerConfig,
							MountPath: dockerConfLocation,
							ReadOnly:  true,
						},
					},
					Args: []string{
						"--dockerfile=/usr/wso2/dockerfile/Dockerfile",
						"--context=/usr/wso2/",
						"--destination=dinushad/" + imageName,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: swaggerVolume,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: apiConfMap,
							},
						},
					},
				},
				{
					Name: dockerConfig,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: dockerConfig,
							},
						},
					},
				},
				{
					Name: mgwDockerFile,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: dockerFile,
							},
						},
					},
				},
			},
		},
	}
}

// generate relevant MGW deployment/services for the given API definition
func createMgwDeployment(cr *wso2v1alpha1.API, imageName string) *appsv1.Deployment {
	labels := map[string]string{
		"app": cr.Name,
	}
	one := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "micro-gateway",
							//todo: docker registry has to be taken from configuration map
							Image: "dinushad/" + imageName,

							Ports: []corev1.ContainerPort{{
								ContainerPort: 80,
							}},
						},
					},
				},
			},
		},
	}
}