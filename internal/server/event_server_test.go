/*
Copyright 2021 The Flux authors

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

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/sethvargo/go-limiter/httplimit"
	"github.com/sethvargo/go-limiter/memorystore"
	prommetrics "github.com/slok/go-http-metrics/metrics/prometheus"
	"github.com/slok/go-http-metrics/middleware"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"

	apiv1 "github.com/fluxcd/notification-controller/api/v1beta2"
)

func TestEventKeyFunc(t *testing.T) {
	g := NewWithT(t)

	// Setup middleware
	store, err := memorystore.New(&memorystore.Config{
		Interval: 10 * time.Minute,
	})
	g.Expect(err).ShouldNot(HaveOccurred())
	middleware, err := httplimit.NewMiddleware(store, eventKeyFunc)
	g.Expect(err).ShouldNot(HaveOccurred())
	handler := middleware.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Make request
	tests := []struct {
		involvedObject corev1.ObjectReference
		severity       string
		message        string
		rateLimit      bool
	}{
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "1",
				Namespace:  "1",
			},
			severity:  eventv1.EventSeverityInfo,
			message:   "Health check passed",
			rateLimit: false,
		},
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "1",
				Namespace:  "1",
			},
			severity:  eventv1.EventSeverityInfo,
			message:   "Health check passed",
			rateLimit: true,
		},
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "1",
				Namespace:  "1",
			},
			severity:  eventv1.EventSeverityError,
			message:   "Health check timed out for [Deployment 'foo/bar']",
			rateLimit: false,
		},
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "2",
				Namespace:  "2",
			},
			severity:  eventv1.EventSeverityInfo,
			message:   "Health check passed",
			rateLimit: false,
		},
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "3",
				Namespace:  "3",
			},
			severity:  eventv1.EventSeverityInfo,
			message:   "Health check passed",
			rateLimit: false,
		},
		{
			involvedObject: corev1.ObjectReference{
				APIVersion: "kustomize.toolkit.fluxcd.io/v1beta1",
				Kind:       "Kustomization",
				Name:       "2",
				Namespace:  "2",
			},
			severity:  eventv1.EventSeverityInfo,
			message:   "Health check passed",
			rateLimit: true,
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%v", i), func(t *testing.T) {
			event := eventv1.Event{
				InvolvedObject: tt.involvedObject,
				Severity:       tt.severity,
				Message:        tt.message,
			}
			eventData, err := json.Marshal(event)
			g.Expect(err).ShouldNot(HaveOccurred())

			req := httptest.NewRequest("POST", "/", bytes.NewBuffer(eventData))
			g.Expect(err).ShouldNot(HaveOccurred())
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if tt.rateLimit {
				g.Expect(res.Code).Should(Equal(429))
				g.Expect(res.Header().Get("X-Ratelimit-Remaining")).Should(Equal("0"))
			} else {
				g.Expect(res.Code).Should(Equal(200))
			}
		})
	}
}

func TestEventServer(t *testing.T) {
	g := NewWithT(t)

	testNamespace := "foo-ns"
	var req *http.Request

	// Run receiver server.
	rcvServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req = r
		w.WriteHeader(200)
	}))
	defer rcvServer.Close()

	provider := &apiv1.Provider{}
	provider.Name = "provider-foo"
	provider.Namespace = testNamespace
	provider.Spec = apiv1.ProviderSpec{
		Type:    "generic",
		Address: rcvServer.URL,
	}

	alert := &apiv1.Alert{}
	alert.Name = "alert-foo"
	alert.Namespace = testNamespace
	alert.Spec = apiv1.AlertSpec{
		ProviderRef:   meta.LocalObjectReference{Name: provider.Name},
		EventSeverity: "info",
		EventSources: []apiv1.CrossNamespaceObjectReference{
			{
				Kind:      "Bucket",
				Name:      "hyacinth",
				Namespace: testNamespace,
			},
			{
				Kind: "Kustomization",
				Name: "*",
			},
			{
				Kind: "GitRepository",
				Name: "*",
				MatchLabels: map[string]string{
					"app": "podinfo",
				},
			},
			{
				Kind:      "Kustomization",
				Name:      "*",
				Namespace: "test",
			},
		},
		ExclusionList: []string{
			"doesnotoccur", // not intended to match
			"excluded",
		},
	}

	// Create objects to be used as involved object in the test events.
	repo1, err := readManifest("./testdata/repo.yaml", testNamespace)
	g.Expect(err).ToNot(HaveOccurred())
	repo2, err := readManifest("./testdata/gitrepo2.yaml", testNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	scheme := runtime.NewScheme()
	g.Expect(apiv1.AddToScheme(scheme)).ToNot(HaveOccurred())
	g.Expect(corev1.AddToScheme(scheme)).ToNot(HaveOccurred())

	// Create a fake kube client with the above objects.
	builder := fakeclient.NewClientBuilder().WithScheme(scheme)
	builder.WithObjects(alert, provider, repo1, repo2)

	// Get a free port to run the event server at.
	l, err := net.Listen("tcp", ":0")
	g.Expect(err).ToNot(HaveOccurred())
	eventServerPort := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	g.Expect(l.Close()).ToNot(HaveOccurred())

	// Create the event server to test.
	eventMdlw := middleware.New(middleware.Config{
		Recorder: prommetrics.NewRecorder(prommetrics.Config{
			Prefix: "gotk_event",
		}),
	})
	store, err := memorystore.New(&memorystore.Config{
		Interval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create memory storage")
	}
	eventServer := NewEventServer("127.0.0.1:"+eventServerPort, logf.Log, builder.Build(), true)
	stopCh := make(chan struct{})
	go eventServer.ListenAndServe(stopCh, eventMdlw, store)
	defer close(stopCh)

	// Create a base event which is copied and mutated in the test cases.
	testEvent := eventv1.Event{
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Bucket",
			Name:      "hyacinth",
			Namespace: testNamespace,
		},
		Severity:            "info",
		Timestamp:           metav1.Now(),
		Message:             "well that happened",
		Reason:              "event-happened",
		ReportingController: "source-controller",
	}

	tests := []struct {
		name            string
		modifyEventFunc func(e *eventv1.Event) *eventv1.Event
		forwarded       bool
	}{
		{
			name:            "forwards when source is a match",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event { return e },
			forwarded:       true,
		},
		{
			name: "drops event when source Kind does not match",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Kind = "GitRepository"
				return e
			},
			forwarded: false,
		},
		{
			name: "drops event when source name does not match",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Name = "slop"
				return e
			},
			forwarded: false,
		},
		{
			name: "drops event when source namespace does not match",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Namespace = "all-buckets"
				return e
			},
			forwarded: false,
		},
		{
			name: "drops event that is matched by exclusion",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.Message = "this is excluded"
				return e
			},
			forwarded: false,
		},
		{
			name: "forwards events when name wildcard is used",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Kind = "Kustomization"
				e.InvolvedObject.Name = "test"
				e.InvolvedObject.Namespace = testNamespace
				e.Message = "test"
				return e
			},
			forwarded: true,
		},
		{
			name: "forwards events when the label matches",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Kind = "GitRepository"
				e.InvolvedObject.Name = "podinfo"
				e.InvolvedObject.APIVersion = "source.toolkit.fluxcd.io/v1beta1"
				e.InvolvedObject.Namespace = testNamespace
				e.Message = "test"
				return e
			},
			forwarded: true,
		},
		{
			name: "drops events when the labels don't match",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Kind = "GitRepository"
				e.InvolvedObject.Name = "podinfo-two"
				e.InvolvedObject.APIVersion = "source.toolkit.fluxcd.io/v1beta1"
				e.InvolvedObject.Namespace = testNamespace
				e.Message = "test"
				return e
			},
			forwarded: false,
		},
		{
			name: "drops events for cross-namespace sources",
			modifyEventFunc: func(e *eventv1.Event) *eventv1.Event {
				e.InvolvedObject.Kind = "Kustomization"
				e.InvolvedObject.Name = "test"
				e.InvolvedObject.Namespace = "test"
				e.Message = "test"
				return e
			},
			forwarded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			event := testEvent.DeepCopy()
			event = tt.modifyEventFunc(event)

			buf := &bytes.Buffer{}
			g.Expect(json.NewEncoder(buf).Encode(event)).To(Succeed())
			res, err := http.Post("http://localhost:"+eventServerPort, "application/json", buf)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(res.StatusCode).To(Equal(202)) // event_server responds with 202 Accepted

			if tt.forwarded {
				g.Eventually(func() bool {
					return req == nil
				}, "2s", "0.1s").Should(BeFalse())
			} else {
				// Check filtered requests.
				//
				// The event_server does forwarding in a goroutine, after
				// responding to the POST of the event. This makes it
				// difficult to know whether the provider has filtered the
				// event, or just not run the goroutine yet. For now, use a
				// timeout (and consistently so it can fail early).
				g.Consistently(func() bool {
					return req == nil
				}, "1s", "0.1s").Should(BeTrue())
			}
			req = nil
		})
	}
}

func readManifest(path, namespace string) (*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	yml := fmt.Sprintf(string(data), namespace)
	reader := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(string(yml)), 2048)
	obj := &unstructured.Unstructured{}
	if err := reader.Decode(obj); err != nil {
		return nil, err
	}
	return obj, nil
}
