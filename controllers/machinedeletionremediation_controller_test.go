package controllers

import (
	"context"
	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	defaultNamespace = "default"
	dummyMachine     = "dummy-machine"
	remediationName  = "test-remediation"
)

var _ = Describe("Default Remediation CR", func() {
	Context("Defaults", func() {
		var underTest *v1alpha1.MachineDeletionRemediation

		BeforeEach(func() {
			underTest = &v1alpha1.MachineDeletionRemediation{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: defaultNamespace},
			}
			err := k8sClient.Create(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			err := k8sClient.Delete(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		When("creating a resource", func() {
			It("CR is namespace scoped", func() {
				Expect(underTest.Namespace).To(Not(BeEmpty()))
			})

		})
	})

	Context("Reconciliation", func() {
		var (
			underTest           *v1alpha1.MachineDeletionRemediation
			objects             []runtime.Object
			reconciler          MachineDeletionRemediationReconciler
			reconcileError      error
			getRemediationError error
		)

		JustBeforeEach(func() {
			cl := fake.NewClientBuilder().WithRuntimeObjects(objects...).Build()
			reconciler = MachineDeletionRemediationReconciler{Client: cl, Log: controllerruntime.Log, Scheme: scheme.Scheme}
			_, reconcileError = reconciler.Reconcile(
				context.Background(),
				controllerruntime.Request{NamespacedName: types.NamespacedName{Name: underTest.Name, Namespace: underTest.Namespace}})
			getRemediationError = reconciler.Get(
				context.Background(),
				client.ObjectKey{Namespace: underTest.Namespace, Name: underTest.Name},
				underTest)
		})

		When("remediation cr exist", func() {

			BeforeEach(func() {
				underTest = &v1alpha1.MachineDeletionRemediation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      remediationName,
						Namespace: defaultNamespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       machineKind,
								APIVersion: "mock.api.version/alpha",
							},
						},
					},
				}
				remediationOwnerMachine := createMachine(underTest)

				nonRelatedMachine := createMachine(underTest)
				nonRelatedMachine.SetName(dummyMachine)

				objects = []runtime.Object{underTest, remediationOwnerMachine, nonRelatedMachine}
			})

			It("Machine referenced from remediation should be deleted", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getRemediationError).NotTo(HaveOccurred())
				tmp := createMachine(underTest)
				err := reconciler.Client.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: remediationName}, tmp)
				Expect(errors.IsNotFound(err)).To(BeTrue())

			})

			It("Machine not referenced from remediation should not be deleted", func() {
				Expect(reconcileError).NotTo(HaveOccurred())
				Expect(getRemediationError).NotTo(HaveOccurred())
				tmp := createMachine(underTest)
				err := reconciler.Client.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: dummyMachine}, tmp)
				Expect(err).NotTo(HaveOccurred())

			})

		})
	})

})

func createMachine(remediation *v1alpha1.MachineDeletionRemediation) *unstructured.Unstructured {
	machine := new(unstructured.Unstructured)
	ownRef := remediation.OwnerReferences[0]
	machine.SetKind(machineKind)
	machine.SetAPIVersion(ownRef.APIVersion)
	machine.SetNamespace(remediation.Namespace)
	machine.SetName(remediation.Name)
	return machine
}
