package controllers

import (
	"context"
	"github.com/medik8s/machine-deletion/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	defaultNamespace = "default"
	dummyMachine     = "dummy-machine"
	remediationName  = "test-remediation"
	mockVersion      = "mock.api.version/alpha"
)

var _ = Describe("Machine Deletion Remediation CR", func() {
	var (
		underTest *v1alpha1.MachineDeletion
	)

	Context("Defaults", func() {

		BeforeEach(func() {
			underTest = &v1alpha1.MachineDeletion{
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
			remediationOwnerMachine *unstructured.Unstructured
			nonRelatedMachine       *unstructured.Unstructured
		)
		JustBeforeEach(func() {
			Expect(k8sClient.Create(context.Background(), underTest)).ToNot(HaveOccurred())
		})
		BeforeEach(func() {
			underTest = &v1alpha1.MachineDeletion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      remediationName,
					Namespace: defaultNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							Kind:       machineKind,
							APIVersion: mockVersion,
							Name:       remediationName,
							UID:        "1",
						},
					},
				},
			}
			remediationOwnerMachine = createMachine(underTest)
			nonRelatedMachine = createMachine(underTest)
			nonRelatedMachine.SetName(dummyMachine)
		})
		AfterEach(func() {
			if err := k8sClient.Delete(context.Background(), remediationOwnerMachine); err != nil {
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}
			if err := k8sClient.Delete(context.Background(), nonRelatedMachine); err != nil {
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}
			Expect(k8sClient.Delete(context.Background(), underTest)).ToNot(HaveOccurred())
		})

		When("remediation cr exist with machine", func() {
			BeforeEach(func() {
				Expect(k8sClient.Create(context.Background(), remediationOwnerMachine)).ToNot(HaveOccurred())
				Expect(k8sClient.Create(context.Background(), nonRelatedMachine)).ToNot(HaveOccurred())
			})

			It("Machine referenced from remediation should be deleted", func() {
				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion(mockVersion)
				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: remediationName}, tmp))
				}, 5*time.Second, 250*time.Millisecond).Should(BeTrue())
			})

			It("Machine not referenced from remediation should not be deleted", func() {
				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion(mockVersion)
				Consistently(func() error {
					return k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: dummyMachine}, tmp)
				}, 5*time.Second, 250*time.Millisecond).ShouldNot(HaveOccurred())
			})

		})

		When("remediation cr exist but machine doesn't exist", func() {

			It("Should not crash when remediation exist but machine does not", func() {
				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion(mockVersion)
				Expect(errors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: remediationName}, tmp))).To(BeTrue())

			})

		})

		When("remediation cr doesn't have a machine owner ref", func() {
			BeforeEach(func() {
				underTest.OwnerReferences = nil
				Expect(k8sClient.Create(context.Background(), remediationOwnerMachine)).ToNot(HaveOccurred())
			})

			It("machine should not be deleted when remediation has no owner ref", func() {

				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion(mockVersion)

				Consistently(func() error {
					return k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: remediationName}, tmp)
				}).ShouldNot(HaveOccurred())

			})

		})

	})

})

func createMachine(remediation *v1alpha1.MachineDeletion) *unstructured.Unstructured {
	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(mockVersion) //remediation.OwnerReferences[0].APIVersion
	machine.SetNamespace(remediation.Namespace)
	machine.SetName(remediation.Name)
	return machine
}
