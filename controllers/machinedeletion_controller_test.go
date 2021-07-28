package controllers

import (
	"context"
	"fmt"
	"github.com/medik8s/machine-deletion/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultNamespace                             = "default"
	dummyMachine                                 = "dummy-machine"
	workerNodeName, masterNodeName               = "worker-node-x", "master-node-x"
	workerNodeMachineName, masterNodeMachineName = "worker-node-x-machine", "master-node-x-machine"
)

var _ = Describe("Machine Deletion Remediation CR", func() {
	var (
		underTest                            *v1alpha1.MachineDeletion
		workerNodeMachine, masterNodeMachine *v1beta1.Machine
		workerNode, masterNode               *v1.Node
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

		BeforeEach(func() {
			workerNodeMachine, masterNodeMachine = createWorkerMachine(workerNodeMachineName), createMachine(masterNodeMachineName)
			workerNode, masterNode = createNode(workerNodeName, workerNodeMachine), createNode(masterNodeName, masterNodeMachine)

			Expect(k8sClient.Create(context.Background(), masterNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), workerNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), masterNodeMachine)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())

		})
		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), masterNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(context.Background(), workerNode)).ToNot(HaveOccurred())
			// ignore error, is deleted by test
			_ = k8sClient.Delete(context.Background(), workerNodeMachine)
			Expect(k8sClient.Delete(context.Background(), masterNodeMachine)).ToNot(HaveOccurred())
		})

		When("remediation does not exist", func() {
			It("No machine is deleted", func() {
				verifyMachineNotDeleted(workerNodeMachineName)
				verifyMachineNotDeleted(masterNodeMachineName)
			})
		})
		When("master node remediation exist", func() {
			BeforeEach(func() {
				underTest = createRemediation(masterNode)
				Expect(k8sClient.Create(context.Background(), underTest)).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				Expect(k8sClient.Delete(context.Background(), underTest)).ToNot(HaveOccurred())
			})
			It("No machine is deleted", func() {
				verifyMachineNotDeleted(workerNodeMachineName)
				verifyMachineNotDeleted(masterNodeMachineName)
			})
		})

		When("worker node remediation exist", func() {
			BeforeEach(func() {
				underTest = createRemediation(workerNode)
				Expect(k8sClient.Create(context.Background(), underTest)).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				Expect(k8sClient.Delete(context.Background(), underTest)).ToNot(HaveOccurred())
			})
			It("worker machine is deleted", func() {
				verifyMachineIsDeleted(workerNodeMachineName)
				verifyMachineNotDeleted(masterNodeMachineName)
			})
		})

	})
})

func createRemediation(node *v1.Node) *v1alpha1.MachineDeletion {
	machineDeletion := &v1alpha1.MachineDeletion{}
	machineDeletion.Name = node.Name
	machineDeletion.Namespace = defaultNamespace
	return machineDeletion
}

func createNode(nodeName string, machine *v1beta1.Machine) *v1.Node {
	n := &v1.Node{}
	n.Name = nodeName
	n.Annotations = map[string]string{}
	n.Annotations[machineAnnotationOpenshift] = fmt.Sprintf("%s/%s", machine.GetNamespace(), machine.GetName())
	return n
}

func createDummyMachine() *v1beta1.Machine {
	return createMachine(dummyMachine)
}
func createMachine(machineName string) *v1beta1.Machine {
	machine := &v1beta1.Machine{}
	machine.SetNamespace(defaultNamespace)
	machine.SetName(machineName)
	return machine
}
func createWorkerMachine(machineName string) *v1beta1.Machine {
	machine := createMachine(machineName)
	ref := metav1.OwnerReference{
		Name:       "machineSetX",
		Kind:       machineSetKind,
		UID:        "1234",
		APIVersion: v1beta1.SchemeGroupVersion.String(),
	}
	machine.SetOwnerReferences([]metav1.OwnerReference{ref})
	return machine
}

func verifyMachineNotDeleted(machineName string) {
	Consistently(
		func() error {
			return k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: machineName}, createDummyMachine())
		}).ShouldNot(HaveOccurred())
}

func verifyMachineIsDeleted(machineName string) {
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: defaultNamespace, Name: machineName}, createDummyMachine()))
	}).Should(BeTrue())
}
