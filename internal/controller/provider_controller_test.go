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

package controller

import (
	"fmt"
	"testing"
	"time"

	"github.com/fluxcd/pkg/runtime/patch"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/fluxcd/notification-controller/api/v1"
	apiv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"
)

func TestProviderReconciler(t *testing.T) {
	g := NewWithT(t)

	timeout := 10 * time.Second

	testns, err := testEnv.CreateNamespace(ctx, "provider-test")
	g.Expect(err).ToNot(HaveOccurred())

	t.Cleanup(func() {
		g.Expect(testEnv.Cleanup(ctx, testns)).ToNot(HaveOccurred())
	})

	provider := &apiv1beta3.Provider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("provider-%s", randStringRunes(5)),
			Namespace: testns.Name,
		},
	}
	providerKey := client.ObjectKeyFromObject(provider)

	t.Run("removed finalizer at create", func(t *testing.T) {
		g := NewWithT(t)

		provider := provider.DeepCopy()
		provider.ObjectMeta.Finalizers = append(provider.ObjectMeta.Finalizers, "foo.bar", apiv1.NotificationFinalizer)
		provider.Spec = apiv1beta3.ProviderSpec{
			Type: "slack",
		}
		g.Expect(testEnv.Create(ctx, provider)).ToNot(HaveOccurred())

		g.Eventually(func() bool {
			_ = testEnv.Get(ctx, providerKey, provider)
			return !hasFinalizer(provider.GetFinalizers())
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("remove finalizer at update", func(t *testing.T) {
		g := NewWithT(t)

		provider := provider.DeepCopy()
		g.Expect(testEnv.Get(ctx, providerKey, provider)).ToNot(HaveOccurred())

		patchHelper, err := patch.NewHelper(provider, testEnv.Client)
		g.Expect(err).ToNot(HaveOccurred())
		provider.ObjectMeta.Finalizers = append(provider.ObjectMeta.Finalizers, apiv1.NotificationFinalizer)
		g.Expect(patchHelper.Patch(ctx, provider)).ToNot(HaveOccurred())

		g.Eventually(func() bool {
			_ = testEnv.Get(ctx, providerKey, provider)
			return !hasFinalizer(provider.GetFinalizers())
		}, timeout, time.Second).Should(BeTrue())
	})

	t.Run("remove finalizer at delete", func(t *testing.T) {
		g := NewWithT(t)

		provider := provider.DeepCopy()
		g.Expect(testEnv.Get(ctx, providerKey, provider)).ToNot(HaveOccurred())

		patchHelper, err := patch.NewHelper(provider, testEnv.Client)
		g.Expect(err).ToNot(HaveOccurred())

		// Suspend the provider to prevent finalizer from getting removed.
		// Ensure only flux finalizer is set to allow the object to be garbage
		// collected at the end.
		provider.ObjectMeta.Finalizers = []string{apiv1.NotificationFinalizer}
		provider.Spec.Suspend = true
		g.Expect(patchHelper.Patch(ctx, provider)).ToNot(HaveOccurred())

		// Verify that finalizer exists on the object using a live client.
		g.Eventually(func() bool {
			_ = k8sClient.Get(ctx, providerKey, provider)
			return hasFinalizer(provider.GetFinalizers())
		}, timeout).Should(BeTrue())

		// Delete the object and verify.
		g.Expect(testEnv.Delete(ctx, provider)).ToNot(HaveOccurred())
		g.Eventually(func() bool {
			if err := testEnv.Get(ctx, providerKey, provider); err != nil {
				return apierrors.IsNotFound(err)
			}
			return false
		}, timeout).Should(BeTrue())
	})
}
