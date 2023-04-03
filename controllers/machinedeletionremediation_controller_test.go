package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	defaultNamespace                                     = "default"
	dummyMachine                                         = "dummy-machine"
	workerNodeName, masterNodeName, noneExistingNodeName = "worker-node-x", "master-node-x", "phantom-node"
	workerNodeMachineName, masterNodeMachineName         = "worker-node-x-machine", "master-node-x-machine"
	mockDeleteFailMessage                                = "mock delete failure"
)

var _ = Describe("Machine Deletion Remediation CR", func() {
	var (
		underTest                            *v1alpha1.MachineDeletionRemediation
		workerNodeMachine, masterNodeMachine *v1beta1.Machine
		workerNode, masterNode               *v1.Node
		//phantomNode is never created by client
		phantomNode *v1.Node
	)

	Context("Defaults", func() {
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
			isDeleteWorkerNodeMachine bool
		)

		Context("Sunny Flows", func() {
			When("remediation does not exist", func() {
				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
				})
				BeforeEach(func() {
					underTest = createRemediation(phantomNode)
				})

			})

			When("remediation associated machine has no owner ref", func() {
				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
				})

				BeforeEach(func() {
					underTest = createRemediation(masterNode)
				})

			})

			When("remediation associated machine has owner ref without controller", func() {
				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
				})

				BeforeEach(func() {
					workerNodeMachine.OwnerReferences[0].Controller = nil
					Expect(k8sClient.Update(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
					underTest = createRemediation(workerNode)
				})

			})

			When("remediation associated machine has owner ref with controller set to false", func() {
				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
				})

				BeforeEach(func() {
					controllerValue := false
					workerNodeMachine.OwnerReferences[0].Controller = &controllerValue
					Expect(k8sClient.Update(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
					underTest = createRemediation(workerNode)
				})

			})

			When("worker node remediation exist", func() {
				It("worker machine is deleted", func() {
					verifyMachineIsDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
				})

				BeforeEach(func() {
					isDeleteWorkerNodeMachine = false
					underTest = createRemediation(workerNode)
				})
			})

		})
		Context("Rainy (Error) Flows", func() {
			var (
				reconcileError   error
				reconcileRequest reconcile.Request
				reconciler       MachineDeletionRemediationReconciler
			)
			When("remediation is not connected to a node", func() {
				It("node not found error", func() {
					Eventually(func() bool {
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return errors.IsNotFound(reconcileError)
					}).Should(BeTrue())
				})

				BeforeEach(func() {
					underTest = createRemediation(phantomNode)
				})
			})

			When("node does not have annotations", func() {
				It("no annotations error", func() {
					Eventually(func() bool {
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return reconcileError != nil && reconcileError.Error() == fmt.Sprintf(noAnnotationsError, underTest.Name)
					}).Should(BeTrue())

				})

				BeforeEach(func() {
					//node is
					underTest = createRemediation(masterNode)
					masterNode.Annotations = nil
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})
			})

			When("node does not have machine annotation", func() {
				It("no machine annotation error", func() {
					Eventually(func() bool {
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return reconcileError != nil && reconcileError.Error() == fmt.Sprintf(noMachineAnnotationError, underTest.Name)
					}).Should(BeTrue())

				})

				BeforeEach(func() {
					//node is
					underTest = createRemediation(masterNode)
					masterNode.Annotations[machineAnnotationOpenshift] = ""
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})
			})

			When("node's machine annotation has invalid value", func() {
				It("failed to extract Name/Namespace from machine annotation error", func() {
					Eventually(func() bool {
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return reconcileError != nil && reconcileError.Error() == fmt.Sprintf(invalidValueMachineAnnotationError, underTest.Name)
					}).Should(BeTrue())

				})

				BeforeEach(func() {
					underTest = createRemediation(masterNode)
					masterNode.Annotations[machineAnnotationOpenshift] = "Gibberish"
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})
			})

			When("node's machine annotation has incorrect value", func() {
				It("failed to fetch machine error", func() {
					Eventually(func() bool {
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return errors.IsNotFound(reconcileError)
					}).Should(BeTrue())

				})

				BeforeEach(func() {
					underTest = createRemediation(masterNode)
					masterNode.Annotations[machineAnnotationOpenshift] = "phantom-machine-namespace/phantom-machine-name"
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})
			})

			When("machine associated to worker node fails deletion", func() {
				It("returns the same delete failure error", func() {
					Skip("Test affected by too many timeouts, skipping it until reworked")
					Eventually(func() bool {
						_ = k8sClient.Create(context.Background(), workerNodeMachine) //make sure worker machine will exist - it may be deleted by first run
						_, reconcileError = reconciler.Reconcile(context.Background(), reconcileRequest)
						return reconcileError != nil && reconcileError.Error() == mockDeleteFailMessage
					}, 10*time.Second, 1*time.Second).Should(BeTrue())
				})

				BeforeEach(func() {
					underTest = createRemediation(workerNode)
					reconciler = MachineDeletionRemediationReconciler{Client: deleteFailClient{k8sClient}, Log: controllerruntime.Log, Scheme: scheme.Scheme}
					isDeleteWorkerNodeMachine = false //Reconcile runs twice, first time is initiated automatically by Ginkgo framework without fake client - the machine is deleted than
				})
			})

			BeforeEach(func() {
				reconciler = MachineDeletionRemediationReconciler{Client: k8sClient, Log: controllerruntime.Log, Scheme: scheme.Scheme}
			})

			JustBeforeEach(func() {
				reconcileRequest = controllerruntime.Request{NamespacedName: types.NamespacedName{Name: underTest.Name, Namespace: defaultNamespace}}
			})

		})

		BeforeEach(func() {
			isDeleteWorkerNodeMachine = true
			workerNodeMachine, masterNodeMachine = createWorkerMachine(workerNodeMachineName), createMachine(masterNodeMachineName)
			workerNode, masterNode, phantomNode = createNodeWithMachine(workerNodeName, workerNodeMachine), createNodeWithMachine(masterNodeName, masterNodeMachine), createNode(noneExistingNodeName)

			Expect(k8sClient.Create(context.Background(), masterNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), workerNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), masterNodeMachine)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			Expect(k8sClient.Create(context.Background(), underTest)).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), masterNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(context.Background(), workerNode)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(context.Background(), masterNodeMachine)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(context.Background(), underTest)).ToNot(HaveOccurred())
			if isDeleteWorkerNodeMachine {
				Expect(k8sClient.Delete(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
			}
		})
	})
})

func createRemediation(node *v1.Node) *v1alpha1.MachineDeletionRemediation {
	mdr := &v1alpha1.MachineDeletionRemediation{}
	mdr.Name = node.Name
	mdr.Namespace = defaultNamespace
	return mdr
}

func createNodeWithMachine(nodeName string, machine *v1beta1.Machine) *v1.Node {
	n := createNode(nodeName)
	n.Annotations[machineAnnotationOpenshift] = fmt.Sprintf("%s/%s", machine.GetNamespace(), machine.GetName())
	return n
}
func createNode(nodeName string) *v1.Node {
	n := &v1.Node{}
	n.Name = nodeName
	n.Annotations = map[string]string{}
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
	controllerVal := true
	machine := createMachine(machineName)
	ref := metav1.OwnerReference{
		Name:       "machineSetX",
		Kind:       machineSetKind,
		UID:        "1234",
		APIVersion: v1beta1.SchemeGroupVersion.String(),
		Controller: &controllerVal,
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

type deleteFailClient struct {
	client.Client
}

func (deleteFailClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return fmt.Errorf(mockDeleteFailMessage)
}
