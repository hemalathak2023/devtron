package models

type UserTerminalSessionRequest struct {
	Id        int    `json:"id"`
	UserId    int32  `json:"userId"`
	ClusterId int    `json:"clusterId" validate:"required"`
	NodeName  string `json:"nodeName" validate:"required"`
	BaseImage string `json:"baseImage" validate:"required"`
	ShellName string `json:"shellName" validate:"required"`
}
type UserTerminalShellSessionRequest struct {
	TerminalAccessId int    `json:"terminalAccessId"`
	ShellName        string `json:"shellName" validate:"required"`
}

type UserTerminalSessionConfig struct {
	MaxSessionPerUser                 int    `env:"MAX_SESSION_PER_USER" envDefault:"5"`
	TerminalPodStatusSyncTimeInSecs   int    `env:"TERMINAL_POD_STATUS_SYNC_In_SECS" envDefault:"600"`
	TerminalPodDefaultNamespace       string `env:"TERMINAL_POD_DEFAULT_NAMESPACE" envDefault:"default"`
	TerminalPodInActiveDurationInMins int    `env:"TERMINAL_POD_INACTIVE_DURATION_IN_MINS" envDefault:"10"`
}

type UserTerminalSessionResponse struct {
	UserTerminalSessionId string            `json:"userTerminalSessionId"`
	UserId                int32             `json:"userId"`
	TerminalAccessId      int               `json:"terminalAccessId"`
	Status                TerminalPodStatus `json:"status"`
	PodName               string            `json:"podName"`
}

const TerminalAccessPodNameTemplate = "terminal-access-" + TerminalAccessClusterIdTemplateVar + "-" + TerminalAccessUserIdTemplateVar + "-" + TerminalAccessRandomIdVar
const TerminalAccessClusterIdTemplateVar = "${cluster_id}"
const TerminalAccessUserIdTemplateVar = "${user_id}"
const TerminalAccessRandomIdVar = "${random_id}"
const TerminalAccessPodNameVar = "${pod_name}"
const TerminalAccessNodeNameVar = "${node_name}"
const TerminalAccessBaseImageVar = "${base_image}"
const TerminalAccessNamespaceVar = "${default_namespace}"
const TerminalAccessPodTemplateName = "terminal-access-pod"
const TerminalAccessRoleTemplateName = "terminal-access-role"
const TerminalAccessClusterRoleBindingTemplateName = "terminal-access-role-binding"
const TerminalAccessClusterRoleBindingTemplate = TerminalAccessPodNameTemplate + "-crb"
const TerminalAccessServiceAccountTemplateName = "terminal-access-service-account"
const TerminalAccessServiceAccountTemplate = TerminalAccessPodNameTemplate + "-sa"

type TerminalPodStatus string

const (
	TerminalPodStarting   TerminalPodStatus = "Starting"
	TerminalPodRunning    TerminalPodStatus = "Running"
	TerminalPodTerminated TerminalPodStatus = "Terminated"
	TerminalPodError      TerminalPodStatus = "Error"
)