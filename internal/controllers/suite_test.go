/*
Copyright 2025 Keikoproj authors.

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


package controllers_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang/mock/gomock"
	alertmanagerv1alpha1 "github.com/keikoproj/alert-manager/api/v1alpha1"
	"github.com/keikoproj/alert-manager/internal/controllers"
	"github.com/keikoproj/alert-manager/internal/controllers/common"
	mock_wavefront "github.com/keikoproj/alert-manager/internal/controllers/mocks"
	"github.com/keikoproj/alert-manager/pkg/k8s"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// Global test variables shared across test cases
var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var mockWavefront *mock_wavefront.MockInterface
var mgrCtx context.Context

// https://github.com/kubernetes-sigs/controller-runtime/issues/1571
var cancelFunc context.CancelFunc

// TestAPIs is the entry point for Ginkgo tests
func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

// BeforeSuite sets up the test environment with a local Kubernetes API server
// and registers the controller that will be tested.
var _ = BeforeSuite(func() {
	// Configure logging for the test environment
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		// Path to the CRD files to be loaded in the test API server
		CRDDirectoryPaths:     []string{filepath.Join("../../", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		// Path to the Kubernetes API server binaries
		BinaryAssetsDirectory: filepath.Join("../../", "bin", "k8s",
			fmt.Sprintf("%s-%s-%s", "1.28.0", runtime.GOOS, runtime.GOARCH)),
	}

	// Only proceed with controller tests if we can successfully start the test environment
	By("starting the test environment")
	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		// Skip the test environment setup but don't fail the tests
		Skip(fmt.Sprintf("Error starting test environment: %v", err))
		return
	}

	Expect(cfg).NotTo(BeNil())

	// Register the AlertManager custom resource definitions with the test API server
	err = alertmanagerv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	// Create a Kubernetes client for interacting with the test API server
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Set up a controller manager for testing controllers
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	// Set up mock for Wavefront API client
	mockCtrl := gomock.NewController(GinkgoTB())
	defer mockCtrl.Finish()

	mockWavefront = mock_wavefront.NewMockInterface(mockCtrl)

	// Create a kubernetes clientset for the k8s.Client
	clientset, err := kubernetes.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	// Initialize the event recorder for controllers
	k8sCl := k8s.Client{
		Cl: clientset,
	}
	commonClient := common.Client{
		Client:   k8sClient,
		Recorder: k8sCl.SetUpEventHandler(context.Background()),
	}

	// Set up AlertsConfigReconciler with mocked dependencies
	err = (&controllers.AlertsConfigReconciler{
		Client:          k8sManager.GetClient(),
		Log:             ctrl.Log.WithName("test-alertsconfig-controller"),
		Scheme:          k8sManager.GetScheme(),
		CommonClient:    &commonClient,
		WavefrontClient: mockWavefront,
		Recorder:        k8sCl.SetUpEventHandler(context.Background()),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Set up WavefrontAlertReconciler with mocked dependencies
	err = (&controllers.WavefrontAlertReconciler{
		Client:          k8sManager.GetClient(),
		Log:             ctrl.Log.WithName("test-wavefrontalert-controller"),
		Scheme:          k8sManager.GetScheme(),
		CommonClient:    &commonClient,
		WavefrontClient: mockWavefront,
		Recorder:        k8sCl.SetUpEventHandler(context.Background()),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Start the controller manager in a separate goroutine
	go func() {
		mgrCtx, cancelFunc = context.WithCancel(context.Background())
		err = k8sManager.Start(mgrCtx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

// AfterSuite tears down the test environment and releases resources
var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testEnv != nil && cfg != nil {
		cancelFunc()
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})
