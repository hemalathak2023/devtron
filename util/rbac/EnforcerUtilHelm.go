package rbac

import (
	"fmt"
	"github.com/devtron-labs/devtron/internal/sql/repository/app"
	repository2 "github.com/devtron-labs/devtron/pkg/appStore/deployment/repository"
	"github.com/devtron-labs/devtron/pkg/cluster/repository"
	"github.com/devtron-labs/devtron/pkg/team"
	"github.com/go-pg/pg"
	"go.uber.org/zap"
	"strings"
)

type EnforcerUtilHelm interface {
	GetHelmObjectByClusterId(clusterId int, namespace string, appName string) string
	GetHelmObjectByTeamIdAndClusterId(teamId int, clusterId int, namespace string, appName string) string
	GetHelmObject(clusterId int, namespace string, appName string) string
	GetAppRBACNameByInstalledAppVersionId(installedAppVersionId int) string
}
type EnforcerUtilHelmImpl struct {
	logger                 *zap.SugaredLogger
	clusterRepository      repository.ClusterRepository
	teamRepository         team.TeamRepository
	appRepository          app.AppRepository
	environmentRepository  repository.EnvironmentRepository
	InstalledAppRepository repository2.InstalledAppRepository
}

func NewEnforcerUtilHelmImpl(logger *zap.SugaredLogger,
	clusterRepository repository.ClusterRepository,
	teamRepository team.TeamRepository,
	appRepository app.AppRepository,
	environmentRepository repository.EnvironmentRepository,
	installedAppRepository repository2.InstalledAppRepository,
) *EnforcerUtilHelmImpl {
	return &EnforcerUtilHelmImpl{
		logger:                 logger,
		clusterRepository:      clusterRepository,
		teamRepository:         teamRepository,
		appRepository:          appRepository,
		environmentRepository:  environmentRepository,
		InstalledAppRepository: installedAppRepository,
	}
}

func (impl EnforcerUtilHelmImpl) GetHelmObjectByClusterId(clusterId int, namespace string, appName string) string {
	cluster, err := impl.clusterRepository.FindById(clusterId)
	if err != nil {
		return fmt.Sprintf("%s/%s/%s", "", "", "")
	}
	return fmt.Sprintf("%s/%s__%s/%s", team.UNASSIGNED_PROJECT, cluster.ClusterName, namespace, strings.ToLower(appName))
}

func (impl EnforcerUtilHelmImpl) GetHelmObjectByTeamIdAndClusterId(teamId int, clusterId int, namespace string, appName string) string {

	cluster, err := impl.clusterRepository.FindById(clusterId)

	teamObj, err := impl.teamRepository.FindOne(teamId)

	if err != nil {
		return fmt.Sprintf("%s/%s/%s", "", "", "")
	}
	return fmt.Sprintf("%s/%s__%s/%s", teamObj.Name, cluster.ClusterName, namespace, strings.ToLower(appName))
}

func (impl EnforcerUtilHelmImpl) GetHelmObject(clusterId int, namespace string, appName string) string {

	installedApp, installedAppErr := impl.InstalledAppRepository.GetInstalledApplicationByClusterIdAndNamespaceAndAppName(clusterId, namespace, appName)

	if installedAppErr != nil && installedAppErr != pg.ErrNoRows {
		impl.logger.Errorw("error on fetching data for rbac object from installed app repository", "err", installedAppErr)
		return ""
	}

	cluster, err := impl.clusterRepository.FindById(clusterId)

	if err != nil {
		impl.logger.Errorw("error on fetching data for rbac object from cluster repository", "err", err)
		return ""
	}

	if installedApp == nil || installedAppErr == pg.ErrNoRows {
		// for cli apps which are not yet linked

		app, err := impl.appRepository.FindAppAndProjectByAppName(appName)

		if err != nil && err != pg.ErrNoRows {
			impl.logger.Errorw("error in fetching app details", "err", err)
			return ""
		}

		if app.TeamId == 0 {
			// case if project is not assigned to cli app
			return fmt.Sprintf("%s/%s__%s/%s", team.UNASSIGNED_PROJECT, cluster.ClusterName, namespace, strings.ToLower(appName))
		} else {
			// case if project is assigned
			return fmt.Sprintf("%s/%s__%s/%s", app.Team.Name, cluster.ClusterName, namespace, strings.ToLower(appName))
		}

	}

	if installedApp.App.TeamId == 0 {
		// for EA apps which have no project assigned to them
		return fmt.Sprintf("%s/%s__%s/%s", team.UNASSIGNED_PROJECT, cluster.ClusterName, namespace, strings.ToLower(appName))
	} else {
		if installedApp.EnvironmentId == 0 {
			// for apps in EA mode, initally env id is 0.
			return fmt.Sprintf("%s/%s__%s/%s", installedApp.App.Team.Name, cluster.ClusterName, namespace, strings.ToLower(appName))
		}
		// for apps which are assigned to a project and have env ID
		return fmt.Sprintf("%s/%s/%s", installedApp.App.Team.Name, installedApp.Environment.EnvironmentIdentifier, strings.ToLower(appName))
	}

}

func (impl EnforcerUtilHelmImpl) GetAppRBACNameByInstalledAppVersionId(installedAppVersionId int) string {

	installedAppVersion, err := impl.InstalledAppRepository.GetInstalledAppVersion(installedAppVersionId)

	if err != nil {
		impl.logger.Errorw("error in fetching installed app version data", "err", err)
		return fmt.Sprintf("%s/%s/%s", "", "", "")
	}

	return fmt.Sprintf("%s/%s/%s", installedAppVersion.InstalledApp.App.Team.Name, installedAppVersion.InstalledApp.Environment.EnvironmentIdentifier, strings.ToLower(installedAppVersion.InstalledApp.App.AppName))

}
