package apim

import (
	wso2v1alpha1 "github.com/wso2/k8s-api-operator/api-operator/pkg/apis/wso2/v1alpha1"
	"github.com/wso2/k8s-api-operator/api-operator/pkg/k8s"
	"github.com/wso2/k8s-api-operator/api-operator/pkg/maps"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"strings"
)

var logDelete = log.Log.WithName("apim.delete")

func DeleteImportedAPI(client *client.Client, instance *wso2v1alpha1.API) error {
	apimConfig := k8s.NewConfMap()
	errApim := k8s.Get(client, types.NamespacedName{Namespace: wso2NameSpaceConst, Name: apimConfName}, apimConfig)

	if errApim != nil {
		if errors.IsNotFound(errApim) {
			logDelete.Info("APIM config is not found. Continue with default configs")
			return errApim
		} else {
			logDelete.Error(errApim, "Error retrieving APIM configs")
			return errApim
		}
	}

	inputConf := k8s.NewConfMap()
	errInput := k8s.Get(client, types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, inputConf)

	if errInput != nil {
		if errors.IsNotFound(errInput) {
			logDelete.Info("API project or swagger not found")
			return errInput
		} else {
			logDelete.Error(errInput, "Error retrieving API configs to import")
			return errInput
		}
	}

	kmEndpoint := apimConfig.Data[apimRegistrationEndpointConst]
	publisherEndpoint := apimConfig.Data[apimPublisherEndpointConst]
	tokenEndpoint := apimConfig.Data[apimTokenEndpointConst]

	if strings.EqualFold(tokenEndpoint, "") {
		tokenEndpoint = kmEndpoint + "/" + defaultTokenEndpoint
		logDelete.Info("Token endpoint not defined. Using keymanager endpoint.", "tokenEndpoint", tokenEndpoint)
	}

	accessToken, errToken := getAccessToken(client, tokenEndpoint, kmEndpoint)
	if errToken != nil {
		return errToken
	}

	if inputConf.BinaryData != nil {
		deleteErr := deleteAPIFromProject(inputConf, accessToken, publisherEndpoint)
		if deleteErr != nil {
			logDelete.Error(deleteErr, "Error when deleting the API using zip")
			return deleteErr
		}
	} else {
		deleteErr := deleteAPIFromSwagger(inputConf, accessToken, publisherEndpoint)
		if deleteErr != nil {
			logDelete.Error(deleteErr, "Error when deleting the API using swagger")
			return deleteErr
		}
	}

	return nil
}

func deleteAPIFromProject(config *corev1.ConfigMap, token string, endpoint string) error {
	zipFileName, errZip := maps.OneKey(config.BinaryData)
	if errZip != nil {
		return errZip
	}
	zippedData := config.BinaryData[zipFileName]

	tmpPath, err := getTempPathOfExtractedArchive(zippedData)
	if err != nil {
		logDelete.Error(err, "Error while getting extracted temporary directory")
		return err
	}

	// Get API info
	apiInfo, err := getAPIDefinition(tmpPath)
	if err != nil {
		logDelete.Error(err, "Error while getting API definition")
		return err
	}

	// checks whether the API exists in APIM
	apiId, err := getAPIId(token, endpoint+"/"+defaultApiListEndpointSuffix, apiInfo.ID.APIName, apiInfo.ID.Version)
	if err != nil {
		return err
	}

	deleteErr := deleteAPIById(endpoint, apiId, token)
	if deleteErr != nil {
		logDelete.Error(deleteErr, "Error when deleting the API from APIM")
	}

	return nil
}

func deleteAPIFromSwagger(config *corev1.ConfigMap, token string, endpoint string) error {
	swaggerFileName, errSwagger := maps.OneKey(config.Data)
	if errSwagger != nil {
		logImport.Error(errSwagger, "Error in the swagger configmap data", "data", config.Data)
		return errSwagger
	}
	swaggerData := config.Data[swaggerFileName]

	_, name, version, err := getAdditionalProperties(swaggerData)
	if err != nil {
		logImport.Error(err, "Error getting additional data")
		return err
	}

	apiId, err := getAPIId(token, endpoint+"/"+defaultApiListEndpointSuffix, name, version)
	if err != nil {
		return err
	}

	deleteErr := deleteAPIById(endpoint, apiId, token)
	if deleteErr != nil {
		logDelete.Error(deleteErr, "Error when deleting the API from APIM")
	}

	return nil
}