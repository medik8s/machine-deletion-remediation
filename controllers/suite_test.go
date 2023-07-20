/*
Copyright 2021.

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
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cclient   customClient
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	plogs     *peekLogger
)

// peekLogger allows to inspect operator's log for testing purpose.
type peekLogger struct {
	logs []string
}

func (p *peekLogger) Write(b []byte) (n int, err error) {
	n, err = GinkgoWriter.Write(b)
	if err != nil {
		return n, err
	}
	p.logs = append(p.logs, string(b))
	return n, err
}

func (p *peekLogger) Contains(s string) bool {
	for _, log := range p.logs {
		if strings.Contains(log, s) {
			return true
		}
	}
	return false
}

func (p *peekLogger) Clear() {
	p.logs = make([]string, 0)
}

// customClient is a Client that can simulate errors
type customClient struct {
	client.Client
	onDeleteError error
}

func (c *customClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.onDeleteError != nil {
		return c.onDeleteError
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	plogs = &peekLogger{logs: make([]string, 0)}
	logf.SetLogger(zap.New(zap.WriteTo(plogs), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "config", "test-crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())

	cclient = customClient{Client: k8sClient}

	err = (&MachineDeletionRemediationReconciler{
		Client: &cclient,
		Log:    ctrl.Log.WithName("controllers").WithName("machine-deletion-controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	v1beta1.AddToScheme(cclient.Scheme())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
