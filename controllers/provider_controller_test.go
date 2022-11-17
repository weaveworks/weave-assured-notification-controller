/*
Copyright 2022 The Flux authors

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

package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/runtime/conditions"

	apiv1 "github.com/fluxcd/notification-controller/api/v1beta2"
)

func TestProviderReconciler_Reconcile(t *testing.T) {
	g := NewWithT(t)
	timeout := 5 * time.Second
	resultP := &apiv1.Provider{}
	namespaceName := "provider-" + randStringRunes(5)
	secretName := "secret-" + randStringRunes(5)

	g.Expect(createNamespace(namespaceName)).NotTo(HaveOccurred(), "failed to create test namespace")

	providerKey := types.NamespacedName{
		Name:      fmt.Sprintf("provider-%s", randStringRunes(5)),
		Namespace: namespaceName,
	}
	provider := &apiv1.Provider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      providerKey.Name,
			Namespace: providerKey.Namespace,
		},
		Spec: apiv1.ProviderSpec{
			Type:    "generic",
			Address: "https://webhook.internal",
		},
	}
	g.Expect(k8sClient.Create(context.Background(), provider)).To(Succeed())

	t.Run("reports ready status", func(t *testing.T) {
		g := NewWithT(t)
		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return resultP.Status.ObservedGeneration == resultP.Generation
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.IsReady(resultP)).To(BeTrue())
		g.Expect(conditions.GetReason(resultP, meta.ReadyCondition)).To(BeIdenticalTo(meta.SucceededReason))

		g.Expect(conditions.Has(resultP, meta.ReconcilingCondition)).To(BeFalse())
		g.Expect(controllerutil.ContainsFinalizer(resultP, apiv1.NotificationFinalizer)).To(BeTrue())
		g.Expect(resultP.Spec.Interval.Duration).To(BeIdenticalTo(10 * time.Minute))
	})

	t.Run("fails with secret not found error", func(t *testing.T) {
		g := NewWithT(t)
		resultP.Spec.SecretRef = &meta.LocalObjectReference{
			Name: secretName,
		}
		g.Expect(k8sClient.Update(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return !conditions.IsReady(resultP)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.GetReason(resultP, meta.ReadyCondition)).To(BeIdenticalTo(apiv1.ValidationFailedReason))
		g.Expect(conditions.GetMessage(resultP, meta.ReadyCondition)).To(ContainSubstring(secretName))

		g.Expect(conditions.Has(resultP, meta.ReconcilingCondition)).To(BeTrue())
		g.Expect(conditions.GetReason(resultP, meta.ReconcilingCondition)).To(BeIdenticalTo(meta.ProgressingWithRetryReason))
		g.Expect(conditions.GetObservedGeneration(resultP, meta.ReconcilingCondition)).To(BeIdenticalTo(resultP.Generation))
		g.Expect(resultP.Status.ObservedGeneration).To(BeIdenticalTo(resultP.Generation - 1))
	})

	t.Run("recovers when secret exists", func(t *testing.T) {
		g := NewWithT(t)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespaceName,
			},
			StringData: map[string]string{
				"token": "test",
			},
		}
		g.Expect(k8sClient.Create(context.Background(), secret)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return conditions.IsReady(resultP)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.GetObservedGeneration(resultP, meta.ReadyCondition)).To(BeIdenticalTo(resultP.Generation))
		g.Expect(resultP.Status.ObservedGeneration).To(BeIdenticalTo(resultP.Generation))
		g.Expect(conditions.Has(resultP, meta.ReconcilingCondition)).To(BeFalse())
	})

	t.Run("handles reconcileAt", func(t *testing.T) {
		g := NewWithT(t)
		reconcileRequestAt := metav1.Now().String()
		resultP.SetAnnotations(map[string]string{
			meta.ReconcileRequestAnnotation: reconcileRequestAt,
		})
		g.Expect(k8sClient.Update(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return resultP.Status.LastHandledReconcileAt == reconcileRequestAt
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("becomes stalled on invalid proxy", func(t *testing.T) {
		g := NewWithT(t)
		resultP.Spec.SecretRef = nil
		resultP.Spec.Proxy = "https://proxy.internal|"
		g.Expect(k8sClient.Update(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return !conditions.IsReady(resultP)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.Has(resultP, meta.ReconcilingCondition)).To(BeFalse())
		g.Expect(conditions.Has(resultP, meta.StalledCondition)).To(BeTrue())
		g.Expect(conditions.GetObservedGeneration(resultP, meta.StalledCondition)).To(BeIdenticalTo(resultP.Generation))
		g.Expect(conditions.GetReason(resultP, meta.StalledCondition)).To(BeIdenticalTo(meta.InvalidURLReason))
		g.Expect(conditions.GetReason(resultP, meta.ReadyCondition)).To(BeIdenticalTo(meta.InvalidURLReason))
	})

	t.Run("recovers from staleness", func(t *testing.T) {
		g := NewWithT(t)
		resultP.Spec.Proxy = "https://proxy.internal"
		g.Expect(k8sClient.Update(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return conditions.IsReady(resultP)
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(conditions.Has(resultP, meta.ReconcilingCondition)).To(BeFalse())
		g.Expect(conditions.Has(resultP, meta.StalledCondition)).To(BeFalse())
	})

	t.Run("finalizes suspended object", func(t *testing.T) {
		g := NewWithT(t)
		resultP.Spec.Suspend = true
		g.Expect(k8sClient.Update(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			_ = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return resultP.Spec.Suspend == true
		}, timeout, time.Second).Should(BeTrue())

		g.Expect(k8sClient.Delete(context.Background(), resultP)).To(Succeed())

		g.Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(provider), resultP)
			return apierrors.IsNotFound(err)
		}, timeout, time.Second).Should(BeTrue())
	})
}

func TestValidateCredentials(t *testing.T) {
	secretName := "foo-secret"
	certSecretName := "cert-secret"
	tests := []struct {
		name           string
		providerSpec   *apiv1.ProviderSpec
		secretData     map[string][]byte
		certSecretData map[string][]byte
		wantErr        bool
	}{
		{
			name: "no address, no secret ref",
			providerSpec: &apiv1.ProviderSpec{
				Type: "slack",
			},
			wantErr: true,
		},
		{
			name: "valid address, no secret ref",
			providerSpec: &apiv1.ProviderSpec{
				Type:    "slack",
				Address: "https://example.com",
			},
		},
		{
			name: "reference to non-existing secret ref",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: "foo"},
			},
			wantErr: true,
		},
		{
			name: "reference to secret with valid address, proxy, headers",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com"),
				"proxy":   []byte("https://exampleproxy.com"),
				"headers": []byte(`foo: bar`),
			},
		},
		{
			name: "reference to secret with invalid address",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com|"),
			},
			wantErr: true,
		},
		{
			name: "reference to secret with invalid proxy",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com"),
				"proxy":   []byte("https://exampleproxy.com|"),
			},
			wantErr: true,
		},
		{
			name: "invalid headers in secret reference",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com"),
				"headers": []byte("foo"),
			},
			wantErr: true,
		},
		{
			name: "invalid spec address overridden by valid secret ref address",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
				Address:   "https://example.com|",
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com"),
			},
		},
		{
			name: "invalid spec proxy overridden by valid secret ref proxy",
			providerSpec: &apiv1.ProviderSpec{
				Type:      "slack",
				SecretRef: &meta.LocalObjectReference{Name: secretName},
				Proxy:     "https://example.com|",
			},
			secretData: map[string][]byte{
				"address": []byte("https://example.com"),
				"proxy":   []byte("https://example.com"),
			},
		},
		{
			name: "reference to non-existing cert secret",
			providerSpec: &apiv1.ProviderSpec{
				Type:          "slack",
				Address:       "https://example.com",
				CertSecretRef: &meta.LocalObjectReference{Name: "some-secret"},
			},
			wantErr: true,
		},
		{
			name: "reference to cert secret without caFile data",
			providerSpec: &apiv1.ProviderSpec{
				Type:          "slack",
				Address:       "https://example.com",
				CertSecretRef: &meta.LocalObjectReference{Name: certSecretName},
			},
			certSecretData: map[string][]byte{
				"aaa": []byte("bbb"),
			},
			wantErr: true,
		},
		{
			name: "cert secret reference with valid CA",
			providerSpec: &apiv1.ProviderSpec{
				Type:          "slack",
				Address:       "https://example.com",
				CertSecretRef: &meta.LocalObjectReference{Name: certSecretName},
			},
			certSecretData: map[string][]byte{
				// Based on https://pkg.go.dev/crypto/tls#X509KeyPair example.
				"caFile": []byte(`-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`),
			},
		},
		{
			name: "cert secret reference with invalid CA",
			providerSpec: &apiv1.ProviderSpec{
				Type:          "slack",
				Address:       "https://example.com",
				CertSecretRef: &meta.LocalObjectReference{Name: certSecretName},
			},
			certSecretData: map[string][]byte{
				"caFile": []byte(`aaaaa`),
			},
			wantErr: true,
		},
		{
			name: "unsupported provider",
			providerSpec: &apiv1.ProviderSpec{
				Type:    "foo",
				Address: "https://example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			builder := fakeclient.NewClientBuilder().WithScheme(testEnv.GetScheme())
			if tt.secretData != nil {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: secretName},
					Data:       tt.secretData,
				}
				builder.WithObjects(secret)
			}
			if tt.certSecretData != nil {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: certSecretName},
					Data:       tt.certSecretData,
				}
				builder.WithObjects(secret)
			}
			r := &ProviderReconciler{
				Client:        builder.Build(),
				EventRecorder: record.NewFakeRecorder(32),
			}
			provider := &apiv1.Provider{Spec: *tt.providerSpec}

			err := r.validateCredentials(context.TODO(), provider)
			g.Expect(err != nil).To(Equal(tt.wantErr))
		})
	}
}
