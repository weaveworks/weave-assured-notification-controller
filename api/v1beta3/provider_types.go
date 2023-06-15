/*
Copyright 2023 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta3

import (
	"time"

	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ProviderKind           string = "Provider"
	GenericProvider        string = "generic"
	GenericHMACProvider    string = "generic-hmac"
	SlackProvider          string = "slack"
	GrafanaProvider        string = "grafana"
	DiscordProvider        string = "discord"
	MSTeamsProvider        string = "msteams"
	RocketProvider         string = "rocket"
	GitHubDispatchProvider string = "githubdispatch"
	GitHubProvider         string = "github"
	GitLabProvider         string = "gitlab"
	GiteaProvider          string = "gitea"
	BitbucketProvider      string = "bitbucket"
	AzureDevOpsProvider    string = "azuredevops"
	GoogleChatProvider     string = "googlechat"
	WebexProvider          string = "webex"
	SentryProvider         string = "sentry"
	AzureEventHubProvider  string = "azureeventhub"
	TelegramProvider       string = "telegram"
	LarkProvider           string = "lark"
	Matrix                 string = "matrix"
	OpsgenieProvider       string = "opsgenie"
	AlertManagerProvider   string = "alertmanager"
)

// ProviderSpec defines the desired state of Provider.
type ProviderSpec struct {
	// Type specifies which Provider implementation to use.
	// +kubebuilder:validation:Enum=slack;discord;msteams;rocket;generic;generic-hmac;github;gitlab;gitea;bitbucket;azuredevops;googlechat;webex;sentry;azureeventhub;telegram;lark;matrix;opsgenie;alertmanager;grafana;githubdispatch;
	// +required
	Type string `json:"type"`

	// Channel specifies the destination channel where events should be posted.
	// +kubebuilder:validation:MaxLength:=2048
	// +optional
	Channel string `json:"channel,omitempty"`

	// Username specifies the name under which events are posted.
	// +kubebuilder:validation:MaxLength:=2048
	// +optional
	Username string `json:"username,omitempty"`

	// Address specifies the HTTP/S incoming webhook address of this Provider.
	// +kubebuilder:validation:Pattern="^(http|https)://.*$"
	// +kubebuilder:validation:MaxLength:=2048
	// +kubebuilder:validation:Optional
	// +optional
	Address string `json:"address,omitempty"`

	// Timeout for sending alerts to the Provider.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m))+$"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Proxy the HTTP/S address of the proxy server.
	// +kubebuilder:validation:Pattern="^(http|https)://.*$"
	// +kubebuilder:validation:MaxLength:=2048
	// +kubebuilder:validation:Optional
	// +optional
	Proxy string `json:"proxy,omitempty"`

	// SecretRef specifies the Secret containing the authentication
	// credentials for this Provider.
	// +optional
	SecretRef *meta.LocalObjectReference `json:"secretRef,omitempty"`

	// CertSecretRef specifies the Secret containing
	// a PEM-encoded CA certificate (`caFile`).
	// +optional
	CertSecretRef *meta.LocalObjectReference `json:"certSecretRef,omitempty"`

	// Suspend tells the controller to suspend subsequent
	// events handling for this Provider.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// Provider is the Schema for the providers API
type Provider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ProviderSpec `json:"spec,omitempty"`
}

//+kubebuilder:object:root=true

// ProviderList contains a list of Provider
type ProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Provider{}, &ProviderList{})
}

// GetTimeout returns the timeout value with a default of 15s for this Provider.
func (in *Provider) GetTimeout() time.Duration {
	duration := 15 * time.Second
	if in.Spec.Timeout != nil {
		duration = in.Spec.Timeout.Duration
	}

	return duration
}
