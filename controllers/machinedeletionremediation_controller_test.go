package controllers

import (
	"context"
	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
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
			underTest *v1alpha1.MachineDeletionRemediation
		)

		When("remediation cr exist", func() {

			var (
				remediationOwnerMachine *unstructured.Unstructured
				nonRelatedMachine       *unstructured.Unstructured
				machineDeleted          bool
			)

			BeforeEach(func() {
				underTest = &v1alpha1.MachineDeletionRemediation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      remediationName,
						Namespace: defaultNamespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       machineKind,
								APIVersion: "mock.api.version/alpha",
								Name:       remediationName,
								UID:        "1",
							},
						},
					},
				}
				remediationOwnerMachine = createMachine(underTest)

				nonRelatedMachine = createMachine(underTest)
				nonRelatedMachine.SetName(dummyMachine)

				Expect(k8sClient.Create(context.Background(), remediationOwnerMachine)).ToNot(HaveOccurred())
				Expect(k8sClient.Create(context.Background(), nonRelatedMachine)).ToNot(HaveOccurred())
				Expect(k8sClient.Create(context.Background(), underTest)).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				if !machineDeleted {
					Expect(k8sClient.Delete(context.Background(), remediationOwnerMachine)).ToNot(HaveOccurred())
				}
				Expect(k8sClient.Delete(context.Background(), nonRelatedMachine)).ToNot(HaveOccurred())
				Expect(k8sClient.Delete(context.Background(), underTest)).ToNot(HaveOccurred())
			})

			It("Machine referenced from remediation should be deleted", func() {
				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion("mock.api.version/alpha")
				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: remediationName}, tmp))
				}, 5*time.Second, 250*time.Millisecond).Should(BeTrue())
				machineDeleted = true
			})

			It("Machine not referenced from remediation should not be deleted", func() {
				tmp := &unstructured.Unstructured{}
				tmp.SetKind(machineKind)
				tmp.SetAPIVersion("mock.api.version/alpha")
				Consistently(func() error {
					return k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: dummyMachine}, tmp)
				}, 5*time.Second, 250*time.Millisecond).ShouldNot(HaveOccurred())
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
