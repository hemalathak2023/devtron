package models

type UserTerminalSessionRequest struct {
	Id        int    `json:"id"`
	UserId    int32  `json:"userId" validate:"required"`
	ClusterId int    `json:"clusterId" validate:"required"`
	NodeName  string `json:"nodeName"`
	BaseImage string `json:"baseImage" validate:"required"`
	ShellName string `json:"shellName" validate:"required"`
}

type UserTerminalSessionConfig struct {
	MaxSessionPerUser               int    `env:"MAX_SESSION_PER_USER" envDefault:"50"`
	TerminalPodStatusSyncTimeInSecs int    `env:"TERMINAL_POD_STATUS_SYNC_In_SECS" envDefault:"5"`
	TerminalPodDefaultNamespace     string `env:"TERMINAL_POD_DEFAULT_NAMESPACE" envDefault:"default"`
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
const TerminalAccessBaseImageVar = "${base_image}"
const TerminalAccessPodTemplateName = "terminal-access-pod"
const TerminalAccessRoleTemplateName = "terminal-access-role"
const TerminalAccessRoleBindingTemplateName = "terminal-access-role-binding"
const TerminalAccessServiceAccountTemplateName = "terminal-access-service-account"

type TerminalPodStatus string

const (
	TerminalPodStarting   TerminalPodStatus = "Starting"
	TerminalPodRunning    TerminalPodStatus = "Running"
	TerminalPodTerminated TerminalPodStatus = "Terminated"
	TerminalPodError      TerminalPodStatus = "Error"
)