package clusterTerminalAccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/caarlos0/env/v6"
	client "github.com/devtron-labs/devtron/api/helm-app"
	"github.com/devtron-labs/devtron/client/k8s/application"
	"github.com/devtron-labs/devtron/internal/sql/models"
	"github.com/devtron-labs/devtron/internal/sql/repository"
	"github.com/devtron-labs/devtron/pkg/terminal"
	"github.com/devtron-labs/devtron/util/k8s"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"strconv"
	"strings"
	"sync"
)

type UserTerminalAccessService interface {
	StartTerminalSession(request *models.UserTerminalSessionRequest) (*models.UserTerminalSessionResponse, error)
	UpdateTerminalSession(request *models.UserTerminalSessionRequest) (*models.UserTerminalSessionResponse, error)
	FetchTerminalStatus(terminalAccessId int) (*models.UserTerminalSessionResponse, error)
	DisconnectTerminalSession(userTerminalSessionId int) error
}

type UserTerminalAccessServiceImpl struct {
	TerminalAccessRepository     repository.TerminalAccessRepository
	Logger                       *zap.SugaredLogger
	Config                       *models.UserTerminalSessionConfig
	TerminalAccessSessionDataMap *map[int]*UserTerminalAccessSessionData
	TerminalAccessDataArrayMutex *sync.RWMutex
	PodStatusSyncCron            *cron.Cron
	k8sApplicationService        k8s.K8sApplicationService
	k8sClientService             application.K8sClientService
	terminalSessionHandler       terminal.TerminalSessionHandler
}

type UserTerminalAccessSessionData struct {
	sessionId                string
	terminalAccessDataEntity *models.UserTerminalAccessData
}

func NewUserTerminalAccessServiceImpl(logger *zap.SugaredLogger, terminalAccessRepository repository.TerminalAccessRepository,
	k8sApplicationService k8s.K8sApplicationService, k8sClientService application.K8sClientService, terminalSessionHandler terminal.TerminalSessionHandler) (*UserTerminalAccessServiceImpl, error) {
	config := &models.UserTerminalSessionConfig{}
	err := env.Parse(config)
	if err != nil {
		return nil, err
	}
	//fetches all running and starting entities from db and start SyncStatus
	podStatusSyncCron := cron.New(
		cron.WithChain())
	terminalAccessDataArrayMutex := &sync.RWMutex{}
	map1 := make(map[int]*UserTerminalAccessSessionData)
	accessServiceImpl := &UserTerminalAccessServiceImpl{
		Logger:                       logger,
		TerminalAccessRepository:     terminalAccessRepository,
		Config:                       config,
		PodStatusSyncCron:            podStatusSyncCron,
		TerminalAccessDataArrayMutex: terminalAccessDataArrayMutex,
		k8sApplicationService:        k8sApplicationService,
		k8sClientService:             k8sClientService,
		TerminalAccessSessionDataMap: &map1,
		terminalSessionHandler:       terminalSessionHandler,
	}
	podStatusSyncCron.Start()
	_, err = podStatusSyncCron.AddFunc(fmt.Sprintf("@every %ds", config.TerminalPodStatusSyncTimeInSecs), accessServiceImpl.SyncPodStatus)
	if err != nil {
		logger.Errorw("error occurred while starting cron job", "time in secs", config.TerminalPodStatusSyncTimeInSecs)
		return nil, err
	}
	go accessServiceImpl.SyncRunningInstances()
	return accessServiceImpl, err
}

func (impl UserTerminalAccessServiceImpl) StartTerminalSession(request *models.UserTerminalSessionRequest) (*models.UserTerminalSessionResponse, error) {
	userId := request.UserId
	terminalAccessDataList, err := impl.TerminalAccessRepository.GetUserTerminalAccessDataByUser(userId)
	if err != nil {
		impl.Logger.Errorw("error occurred while getting terminal access data for user id", "userId", userId, "err", err)
		return nil, err
	}
	// check for max session check
	maxSessionPerUser := impl.Config.MaxSessionPerUser
	userRunningSessionCount := len(terminalAccessDataList)
	if userRunningSessionCount >= maxSessionPerUser {
		errStr := fmt.Sprintf("cannot start new session more than configured %s", strconv.Itoa(maxSessionPerUser))
		impl.Logger.Errorw(errStr, "req", request)
		return nil, errors.New(errStr)
	}
	maxIdForUser := getMaxId(terminalAccessDataList)
	podName, err := impl.startTerminalPod(request, maxIdForUser)
	return impl.createTerminalEntity(request, podName, err)
}

func getMaxId(userAccessList []*models.UserTerminalAccessData) int {
	maxId := 0
	for _, accessData := range userAccessList {
		accessId := accessData.Id
		if accessId > maxId {
			maxId = accessId
		}
	}
	return maxId
}

func (impl UserTerminalAccessServiceImpl) createTerminalEntity(request *models.UserTerminalSessionRequest, podName string, err error) (*models.UserTerminalSessionResponse, error) {
	userAccessData := &models.UserTerminalAccessData{
		UserId:    request.UserId,
		ClusterId: request.ClusterId,
		NodeName:  request.NodeName,
		Status:    string(models.TerminalPodStarting),
		PodName:   podName,
		Metadata:  impl.extractMetadataString(request),
	}
	err = impl.TerminalAccessRepository.SaveUserTerminalAccessData(userAccessData)
	if err != nil {
		impl.Logger.Errorw("error occurred while saving user terminal access data", "err", err)
		return nil, err
	}
	impl.TerminalAccessDataArrayMutex.Lock()
	terminalAccessDataArray := *impl.TerminalAccessSessionDataMap
	terminalAccessDataArray[userAccessData.Id] = &UserTerminalAccessSessionData{terminalAccessDataEntity: userAccessData}
	impl.TerminalAccessSessionDataMap = &terminalAccessDataArray
	impl.TerminalAccessDataArrayMutex.Unlock()
	return &models.UserTerminalSessionResponse{
		UserId:           userAccessData.UserId,
		PodName:          podName,
		TerminalAccessId: userAccessData.Id,
	}, nil
}

func (impl UserTerminalAccessServiceImpl) UpdateTerminalSession(request *models.UserTerminalSessionRequest) (*models.UserTerminalSessionResponse, error) {
	userTerminalAccessId := request.Id
	terminalAccessData, err := impl.TerminalAccessRepository.GetUserTerminalAccessData(userTerminalAccessId)
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching user terminal access data", "userTerminalAccessId", userTerminalAccessId, "err", err)
		return nil, err
	}
	podName := terminalAccessData.PodName
	//err = impl.DeleteTerminalPod(terminalAccessData.ClusterId, podName)
	err = impl.DisconnectTerminalSession(userTerminalAccessId)
	if err != nil {
		impl.Logger.Errorw("error occurred while deleting terminal pod", "userTerminalAccessId", userTerminalAccessId, "err", err)
		return nil, err
	}
	terminalAccessPodTemplate, err := impl.TerminalAccessRepository.FetchTerminalAccessTemplate(models.TerminalAccessPodTemplateName)
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching template", "template", models.TerminalAccessPodTemplateName, "err", err)
		return nil, err
	}
	userId := request.UserId
	terminalAccessDataList, err := impl.TerminalAccessRepository.GetUserTerminalAccessDataByUser(userId)
	if err != nil {
		impl.Logger.Errorw("error occurred while getting terminal access data for user id", "userId", userId, "err", err)
		return nil, err
	}
	maxId := getMaxId(terminalAccessDataList)
	podName = impl.createPodName(request, maxId)
	err = impl.applyTemplateData(request, podName, terminalAccessPodTemplate)
	if err != nil {
		return nil, err
	}
	return impl.createTerminalEntity(request, podName, err)
}

func (impl UserTerminalAccessServiceImpl) checkTerminalExists(userTerminalSessionId int) (*models.UserTerminalAccessData, error) {
	terminalAccessData, err := impl.TerminalAccessRepository.GetUserTerminalAccessData(userTerminalSessionId)
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching user terminal access data", "userTerminalSessionId", userTerminalSessionId, "err", err)
		return nil, err
	}
	terminalStatus := terminalAccessData.Status
	terminalPodStatus := models.TerminalPodStatus(terminalStatus)
	if terminalPodStatus == models.TerminalPodTerminated || terminalPodStatus == models.TerminalPodError {
		impl.Logger.Errorw("pod is already in terminated/error state", "userTerminalSessionId", userTerminalSessionId, "terminalPodStatus", terminalPodStatus)
		return nil, errors.New("pod already terminated")
	}
	return terminalAccessData, nil
}

func (impl UserTerminalAccessServiceImpl) DisconnectTerminalSession(userTerminalAccessId int) error {
	terminalAccessData, err := impl.checkTerminalExists(userTerminalAccessId)
	if err != nil {
		return err
	}
	// disconnect session first
	accessSessionDataMap := *impl.TerminalAccessSessionDataMap
	accessSessionData, present := accessSessionDataMap[userTerminalAccessId]
	if present {
		impl.Logger.Infow("closing socket connection", "userTerminalAccessId", userTerminalAccessId)
		impl.terminalSessionHandler.Close(accessSessionData.sessionId, 1, "Process exited")
	}
	// handle already terminated/not found cases
	err = impl.DeleteTerminalPod(terminalAccessData.ClusterId, terminalAccessData.PodName)
	if err != nil {
		impl.Logger.Errorw("error occurred while stopping terminal pod", "userTerminalAccessId", userTerminalAccessId, "err", err)
		return err
	}
	return err
}

func (impl UserTerminalAccessServiceImpl) extractMetadataString(request *models.UserTerminalSessionRequest) string {
	metadata := make(map[string]string)
	metadata["BaseImage"] = request.BaseImage
	metadata["ShellName"] = request.ShellName
	metadataJsonBytes, err := json.Marshal(metadata)
	if err != nil {
		impl.Logger.Errorw("error occurred while converting metadata to json", "request", request, "err", err)
		return "{}"
	}
	return string(metadataJsonBytes)
}

func (impl UserTerminalAccessServiceImpl) getMetadataMap(metadata string) (map[string]string, error) {
	var metadataMap map[string]string
	err := json.Unmarshal([]byte(metadata), &metadataMap)
	if err != nil {
		impl.Logger.Errorw("error occurred while converting metadata to map", "metadata", metadata, "err", err)
		return nil, err
	}
	return metadataMap, nil
}

func (impl UserTerminalAccessServiceImpl) startTerminalPod(request *models.UserTerminalSessionRequest, runningCount int) (string, error) {
	podNameVar := impl.createPodName(request, runningCount)
	accessTemplates, err := impl.TerminalAccessRepository.FetchAllTemplates()
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching terminal access templates", "err", err)
		return "", err
	}
	for _, accessTemplate := range accessTemplates {
		err = impl.applyTemplateData(request, podNameVar, accessTemplate)
		if err != nil {
			return "", err
		}
	}
	return podNameVar, err
}

func (impl UserTerminalAccessServiceImpl) createPodName(request *models.UserTerminalSessionRequest, runningCount int) string {
	podNameVar := models.TerminalAccessPodNameTemplate
	podNameVar = strings.ReplaceAll(podNameVar, models.TerminalAccessClusterIdTemplateVar, strconv.Itoa(request.ClusterId))
	podNameVar = strings.ReplaceAll(podNameVar, models.TerminalAccessUserIdTemplateVar, strconv.FormatInt(int64(request.UserId), 10))
	podNameVar = strings.ReplaceAll(podNameVar, models.TerminalAccessRandomIdVar, strconv.Itoa(runningCount+1))
	return podNameVar
}

func (impl UserTerminalAccessServiceImpl) applyTemplateData(request *models.UserTerminalSessionRequest, podNameVar string, terminalTemplate *models.TerminalAccessTemplates) error {

	templateData := terminalTemplate.TemplateData
	clusterId := request.ClusterId
	templateData = strings.ReplaceAll(templateData, models.TerminalAccessClusterIdTemplateVar, strconv.Itoa(clusterId))
	templateData = strings.ReplaceAll(templateData, models.TerminalAccessUserIdTemplateVar, strconv.FormatInt(int64(request.UserId), 10))
	templateData = strings.ReplaceAll(templateData, models.TerminalAccessPodNameVar, podNameVar)
	templateData = strings.ReplaceAll(templateData, models.TerminalAccessBaseImageVar, request.BaseImage)
	err := impl.applyTemplate(clusterId, terminalTemplate.TemplateKindData, templateData)
	if err != nil {
		impl.Logger.Errorw("error occurred while applying template ", "name", terminalTemplate.TemplateName, "err", err)
		return err
	}
	return nil
}

func (impl UserTerminalAccessServiceImpl) SyncPodStatus() {
	// set starting/running pods in memory and fetch status of those pods and update their status in Db
	terminalAccessPodTemplate, err := impl.TerminalAccessRepository.FetchTerminalAccessTemplate(models.TerminalAccessPodTemplateName)
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching template", "template", models.TerminalAccessPodTemplateName, "err", err)
		return
	}
	terminalAccessDataMap := *impl.TerminalAccessSessionDataMap
	for _, terminalAccessSessionData := range terminalAccessDataMap {
		terminalAccessData := terminalAccessSessionData.terminalAccessDataEntity
		clusterId := terminalAccessData.ClusterId
		terminalAccessPodName := terminalAccessData.PodName
		existingStatus := terminalAccessData.Status
		terminalPodStatus, err := impl.getPodStatus(clusterId, terminalAccessPodName, terminalAccessPodTemplate.TemplateKindData)
		if err != nil {
			continue
		}
		terminalPodStatusString := terminalPodStatus
		if existingStatus != terminalPodStatusString {
			terminalAccessId := terminalAccessData.Id
			err = impl.TerminalAccessRepository.UpdateUserTerminalStatus(terminalAccessId, terminalPodStatusString)
			if err != nil {
				impl.Logger.Errorw("error occurred while updating terminal status", "terminalAccessId", terminalAccessId, "err", err)
				continue
			}
			terminalAccessData.Status = terminalPodStatusString
			//create terminal session if status is Running and store sessionId
			metadata := terminalAccessData.Metadata
			metadataMap, err := impl.getMetadataMap(metadata)
			if err != nil {
				impl.Logger.Errorw("error occurred while converting metadata json to map", "metadata", metadata, "err", err)
				continue
			}
			request := &terminal.TerminalSessionRequest{
				Shell:     metadataMap["ShellName"],
				Namespace: impl.Config.TerminalPodDefaultNamespace,
				PodName:   terminalAccessPodName,
				ClusterId: clusterId,
			}
			_, terminalMessage, err := impl.terminalSessionHandler.GetTerminalSession(request)
			if err != nil {
				impl.Logger.Errorw("error occurred while creating terminal session", "terminalAccessId", terminalAccessId, "err", err)
				continue
			}
			sessionID := terminalMessage.SessionID
			terminalAccessSessionData.sessionId = sessionID
		}
	}
	impl.TerminalAccessDataArrayMutex.Lock()
	for _, terminalAccessSessionData := range terminalAccessDataMap {
		terminalAccessData := terminalAccessSessionData.terminalAccessDataEntity
		if terminalAccessData.Status != string(models.TerminalPodStarting) && terminalAccessData.Status != string(models.TerminalPodRunning) {
			delete(terminalAccessDataMap, terminalAccessData.Id)
		}
	}
	impl.TerminalAccessSessionDataMap = &terminalAccessDataMap
	impl.TerminalAccessDataArrayMutex.Unlock()
}

func (impl UserTerminalAccessServiceImpl) checkAndStartSession(terminalAccessData *models.UserTerminalAccessData, terminalAccessPodTemplate *models.TerminalAccessTemplates) (string, error) {
	clusterId := terminalAccessData.ClusterId
	terminalAccessPodName := terminalAccessData.PodName
	existingStatus := terminalAccessData.Status
	terminalPodStatusString, err := impl.getPodStatus(clusterId, terminalAccessPodName, terminalAccessPodTemplate.TemplateKindData)
	if err != nil {
		return "", err
	}
	sessionID := ""
	terminalAccessId := terminalAccessData.Id
	if terminalPodStatusString == string(models.TerminalPodRunning) {
		if existingStatus != terminalPodStatusString {
			err = impl.TerminalAccessRepository.UpdateUserTerminalStatus(terminalAccessId, terminalPodStatusString)
			if err != nil {
				impl.Logger.Errorw("error occurred while updating terminal status", "terminalAccessId", terminalAccessId, "err", err)
				return "", err
			}
			terminalAccessData.Status = terminalPodStatusString
		}
		//create terminal session if status is Running and store sessionId
		metadata := terminalAccessData.Metadata
		metadataMap, err := impl.getMetadataMap(metadata)
		if err != nil {
			impl.Logger.Errorw("error occurred while converting metadata json to map", "metadata", metadata, "err", err)
			return "", err
		}
		request := &terminal.TerminalSessionRequest{
			Shell:     metadataMap["ShellName"],
			Namespace: impl.Config.TerminalPodDefaultNamespace,
			PodName:   terminalAccessPodName,
			ClusterId: clusterId,
		}
		_, terminalMessage, err := impl.terminalSessionHandler.GetTerminalSession(request)
		if err != nil {
			impl.Logger.Errorw("error occurred while creating terminal session", "terminalAccessId", terminalAccessId, "err", err)
			return "", err
		}
		sessionID = terminalMessage.SessionID
	}
	return sessionID, err
}

func (impl UserTerminalAccessServiceImpl) FetchTerminalStatus(terminalAccessId int) (*models.UserTerminalSessionResponse, error) {

	// check for existing session id and its state, if active then return this sessionId only
	terminalAccessDataMap := *impl.TerminalAccessSessionDataMap
	terminalAccessSessionData, present := terminalAccessDataMap[terminalAccessId]
	var terminalSessionId = ""
	var terminalAccessData *models.UserTerminalAccessData
	if present {
		terminalSessionId = terminalAccessSessionData.sessionId
		validSession := impl.terminalSessionHandler.ValidateSession(terminalSessionId)
		if validSession {
			terminalAccessData = terminalAccessSessionData.terminalAccessDataEntity
		}
	}
	if terminalAccessData == nil {
		terminalAccessPodTemplate, err := impl.TerminalAccessRepository.FetchTerminalAccessTemplate(models.TerminalAccessPodTemplateName)
		if err != nil {
			impl.Logger.Errorw("error occurred while fetching template", "template", models.TerminalAccessPodTemplateName, "err", err)
			return nil, err
		}
		terminalAccessData, err = impl.TerminalAccessRepository.GetUserTerminalAccessData(terminalAccessId)
		if err != nil {
			impl.Logger.Errorw("error occurred while fetching terminal status", "terminalAccessId", terminalAccessId, "err", err)
			return nil, err
		}
		terminalSessionId, err = impl.checkAndStartSession(terminalAccessData, terminalAccessPodTemplate)
		if err != nil {
			return nil, err
		}
		impl.TerminalAccessDataArrayMutex.Lock()
		terminalAccessSessionData.sessionId = terminalSessionId
		terminalAccessSessionData.terminalAccessDataEntity = terminalAccessData
		impl.TerminalAccessDataArrayMutex.Unlock()
	}
	terminalAccessDataId := terminalAccessData.Id

	terminalAccessResponse := &models.UserTerminalSessionResponse{
		TerminalAccessId:      terminalAccessDataId,
		UserId:                terminalAccessData.UserId,
		Status:                models.TerminalPodStatus(terminalAccessData.Status),
		PodName:               terminalAccessData.PodName,
		UserTerminalSessionId: terminalSessionId,
	}
	return terminalAccessResponse, nil
}

func (impl UserTerminalAccessServiceImpl) DeleteTerminalPod(clusterId int, terminalPodName string) error {
	//make pod delete request, handle errors if pod does  not exists
	restConfig, err := impl.k8sApplicationService.GetRestConfigByClusterId(clusterId)
	if err != nil {
		return err
	}

	terminalAccessPodTemplate, err := impl.TerminalAccessRepository.FetchTerminalAccessTemplate(models.TerminalAccessPodTemplateName)
	if err != nil {
		impl.Logger.Errorw("error occurred while fetching template", "template", models.TerminalAccessPodTemplateName, "err", err)
		return err
	}

	gvkDataString := terminalAccessPodTemplate.TemplateKindData
	var gvkData map[string]string
	err = json.Unmarshal([]byte(gvkDataString), &gvkData)
	if err != nil {
		impl.Logger.Errorw("error occurred while extracting data for gvk", "gvkDataString", gvkDataString, "err", err)
		return err
	}

	k8sRequest := &application.K8sRequestBean{
		ResourceIdentifier: application.ResourceIdentifier{
			Name:      terminalPodName,
			Namespace: impl.Config.TerminalPodDefaultNamespace,
			GroupVersionKind: schema.GroupVersionKind{
				Group:   gvkData["group"],
				Version: gvkData["version"],
				Kind:    gvkData["kind"],
			},
		},
	}
	_, err = impl.k8sClientService.DeleteResource(restConfig, k8sRequest)
	if err != nil {
		impl.Logger.Errorw("error occurred while deleting resource for pod", "podName", terminalPodName, "err", err)
	}
	return err
}

func (impl UserTerminalAccessServiceImpl) applyTemplate(clusterId int, gvkDataString string, templateData string) error {
	restConfig, err := impl.k8sApplicationService.GetRestConfigByClusterId(clusterId)
	if err != nil {
		return err
	}

	var gvkData map[string]string
	err = json.Unmarshal([]byte(gvkDataString), &gvkData)
	if err != nil {
		impl.Logger.Errorw("error occurred while extracting data for gvk", "gvkDataString", gvkDataString, "err", err)
		return err
	}

	k8sRequest := &application.K8sRequestBean{
		ResourceIdentifier: application.ResourceIdentifier{
			Namespace: impl.Config.TerminalPodDefaultNamespace,
			GroupVersionKind: schema.GroupVersionKind{
				Group:   gvkData["group"],
				Version: gvkData["version"],
				Kind:    gvkData["kind"],
			},
		},
	}

	_, err = impl.k8sClientService.CreateResource(restConfig, k8sRequest, templateData)
	if err != nil && err.(*k8sErrors.StatusError).Status().Reason != "AlreadyExists" {
		impl.Logger.Errorw("error in creating resource", "err", err, "request", k8sRequest)
		return err
	}
	return nil
}

func (impl UserTerminalAccessServiceImpl) getPodStatus(clusterId int, podName string, gvkDataString string) (string, error) {
	// return terminated if pod does not exist
	var gvkData map[string]string
	err := json.Unmarshal([]byte(gvkDataString), &gvkData)
	if err != nil {
		impl.Logger.Errorw("error occurred while extracting data for gvk", "gvkDataString", gvkDataString, "err", err)
		return "", err
	}
	request := &k8s.ResourceRequestBean{
		AppIdentifier: &client.AppIdentifier{
			ClusterId: clusterId,
		},
		K8sRequest: &application.K8sRequestBean{
			ResourceIdentifier: application.ResourceIdentifier{
				Name:      podName,
				Namespace: impl.Config.TerminalPodDefaultNamespace,
				GroupVersionKind: schema.GroupVersionKind{
					Group:   gvkData["group"],
					Version: gvkData["version"],
					Kind:    gvkData["kind"],
				},
			},
		},
	}
	response, err := impl.k8sApplicationService.GetResource(request)
	if err != nil {
		if err.(*k8sErrors.StatusError).Status().Reason == "NotFound" {
			return string(models.TerminalPodTerminated), nil
		} else {
			impl.Logger.Errorw("error occurred while fetching resource info for pod", "podName", podName)
			return "", err
		}
	}
	status := ""
	if response != nil {
		manifest := response.Manifest
		for key, value := range manifest.Object {
			if key == "status" {
				statusData := value.(map[string]interface{})
				status = statusData["phase"].(string)
			}
		}
	}
	impl.Logger.Debug("pod status", "podName", podName, "status", status)
	return status, nil
}

func (impl UserTerminalAccessServiceImpl) SyncRunningInstances() {
	terminalAccessData, err := impl.TerminalAccessRepository.GetAllUserTerminalAccessData()
	if err != nil {
		impl.Logger.Fatalw("error occurred while fetching all running/starting data", "err", err)
	}
	impl.TerminalAccessDataArrayMutex.Lock()
	terminalAccessDataMap := *impl.TerminalAccessSessionDataMap
	for _, accessData := range terminalAccessData {
		terminalAccessDataMap[accessData.Id] = &UserTerminalAccessSessionData{
			terminalAccessDataEntity: accessData,
		}
	}
	impl.TerminalAccessSessionDataMap = &terminalAccessDataMap
	impl.TerminalAccessDataArrayMutex.Unlock()
}