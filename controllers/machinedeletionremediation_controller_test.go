package controllers

import (
	"context"
	"fmt"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonconditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machinev1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	defaultNamespace                                                     = "default"
	machineNamespace                                                     = "openshift-machine-api"
	machineSetName, machineSetNameZeroReplicas                           = "machine-set-x", "machine-set-x-zero-replicas"
	machineSetKind                                                       = "MachineSet"
	cpmsName                                                             = "cpms-x"
	cpmsKind                                                             = "ControlPlaneMachineSet"
	dummyMachine                                                         = "dummy-machine"
	workerNodeName, masterNodeName, cpNodeWithOwnerName, phantomNodeName = "worker-node-x", "master-node-x", "cp-node-x", "phantom-node"
	workerNodeMachineName, masterNodeMachineName, cpNodeMachineName      = "worker-node-x-machine", "master-node-x-machine", "control-plane-node-x-machine"
	phantomNodeMachineName                                               = "phantom-node-machine"
	mockDeleteFailMessage                                                = "mock delete failure"
	noMachineDeletionRemediationCRFound                                  = "noMachineDeletionRemediationCRFound"
	processingConditionNotSetError                                       = "ProcessingConditionNotSet"
	processingConditionSetButNoMatchError                                = "ProcessingConditionSetButNoMatch"
	processingConditionSetAndMatchSuccess                                = "ProcessingConditionSetAndMatch"
	processingConditionSetButWrongReasonError                            = "processingConditionSetButWrongReason"
	processingConditionStartedInfo                                       = "{\"processingConditionStatus\": \"True\", \"succededConditionStatus\": \"Unknown\", \"reason\": \"RemediationStarted\"}"
)

var underTest *v1alpha1.MachineDeletionRemediation

type expectedCondition struct {
	conditionType   string
	conditionStatus metav1.ConditionStatus
	conditionReason conditionChangeReason
}

var _ = Describe("Machine Deletion Remediation CR", func() {
	var (
		machineSet, machineSetZeroReplicas                                      *machinev1beta1.MachineSet
		cpms                                                                    *machinev1.ControlPlaneMachineSet
		workerNodeMachine, masterNodeMachine, cpNodeMachine, phantomNodeMachine *machinev1beta1.Machine
		workerNode, masterNode                                                  *v1.Node
		cpNodeWithOwnerList                                                     []v1.Node
		//phantomNode is never created by client
		phantomNode *v1.Node
	)

	Context("Defaults", func() {
		BeforeEach(func() {
			underTest = &v1alpha1.MachineDeletionRemediation{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: defaultNamespace},
			}
			Expect(k8sClient.Create(context.Background(), underTest)).To(Succeed())
			DeferCleanup(k8sClient.Delete, underTest)
		})

		When("creating a resource", func() {
			It("CR is namespace scoped", func() {
				Expect(underTest.Namespace).To(Not(BeEmpty()))
			})
		})
	})

	Context("Reconciliation", func() {
		BeforeEach(func() {
			plogs.Clear()

			machineSet = createMachineSet(machineSetName, 1)
			machineSetZeroReplicas = createMachineSet(machineSetNameZeroReplicas, 0)
			cpms = createControlPlaneMachineSet(cpmsName)

			workerNodeMachine = createMachineWithOwner(workerNodeMachineName, machineSet)
			phantomNodeMachine = createMachineWithOwner(phantomNodeMachineName, machineSetZeroReplicas)
			masterNodeMachine = createMachine(masterNodeMachineName)
			cpNodeMachine = createMachineWithOwner(cpNodeMachineName, cpms)

			workerNode, masterNode, phantomNode =
				createNodeWithMachine(workerNodeName, workerNodeMachine),
				createNodeWithMachine(masterNodeName, masterNodeMachine),
				createNode(phantomNodeName)

			// CPMS's ReplicaSet has minimum value of 3, so we need 3 CP nodes
			for i := 0; i < 3; i++ {
				cpNode := createNodeWithMachine(fmt.Sprintf("%s-%d", cpNodeWithOwnerName, i), cpNodeMachine)
				cpNodeWithOwnerList = append(cpNodeWithOwnerList, *cpNode)
				Expect(k8sClient.Create(context.Background(), cpNode)).To(Succeed())
				DeferCleanup(k8sClient.Delete, cpNode)
			}

			Expect(k8sClient.Create(context.Background(), machineSet)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), machineSetZeroReplicas)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), cpms)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), masterNode)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), workerNode)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), masterNodeMachine)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), cpNodeMachine)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), workerNodeMachine)).To(Succeed())
			Expect(k8sClient.Create(context.Background(), phantomNodeMachine)).To(Succeed())

			DeferCleanup(k8sClient.Delete, machineSet)
			DeferCleanup(k8sClient.Delete, machineSetZeroReplicas)
			DeferCleanup(k8sClient.Delete, cpms)
			DeferCleanup(k8sClient.Delete, masterNode)
			DeferCleanup(k8sClient.Delete, workerNode)
			DeferCleanup(k8sClient.Delete, masterNodeMachine)

			// The following Machines are expected to be deleted in some tests
			// so do not error if they are not found
			DeferCleanup(deleteIgnoreNotFound(), cpNodeMachine)
			DeferCleanup(deleteIgnoreNotFound(), workerNodeMachine)
			DeferCleanup(deleteIgnoreNotFound(), phantomNodeMachine)
		})

		JustBeforeEach(func() {
			Expect(k8sClient.Create(context.Background(), underTest)).To(Succeed())
			DeferCleanup(k8sClient.Delete, underTest)
		})

		Context("Sunny Flows", func() {
			When("node does not exist", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(phantomNode.Name)
				})

				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedNodeNotFound},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedNodeNotFound}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedNodeNotFound", "failed to fetch node", true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("remediation associated machine has no owner ref", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(masterNode.Name)
				})

				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedNoControllerOwner", noControllerOwnerErrorMsg, true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("remediation associated machine has owner ref without controller", func() {
				BeforeEach(func() {
					workerNodeMachine.OwnerReferences[0].Controller = nil
					Expect(k8sClient.Update(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
					underTest = createRemediationOwnedByNHC(workerNode.Name)
				})

				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedNoControllerOwner", noControllerOwnerErrorMsg, true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("remediation associated machine has owner ref with controller set to false", func() {
				BeforeEach(func() {
					controllerValue := false
					workerNodeMachine.OwnerReferences[0].Controller = &controllerValue
					Expect(k8sClient.Update(context.Background(), workerNodeMachine)).ToNot(HaveOccurred())
					underTest = createRemediationOwnedByNHC(workerNode.Name)
				})

				It("No machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedNoControllerOwner},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})

					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedNoControllerOwner", noControllerOwnerErrorMsg, true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("remediation associated machine has valid owner ref of CPMS Kind", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(cpNodeWithOwnerList[0].Name)
				})

				It("CP machine is deleted", func() {
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)
					verifyMachineIsDeleted(cpNodeMachineName)

					// Machine is deleted, but the remediation is not completed yet
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionTrue, remediationStarted},
						{commonconditions.SucceededType, metav1.ConditionUnknown, remediationStarted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})

					// Mock Machine and Nodes re-provisioning (even though this test does not actually delete the nodes, just the machine).
					// 1. Create a Machine's replacement with a new name
					// 2. Update Nodes' annotation to point to the new Machine
					replacementName := cpNodeMachineName + "-replacement"
					replacement := createMachineWithOwner(replacementName, cpms)
					Expect(k8sClient.Create(context.Background(), replacement)).To(Succeed())
					DeferCleanup(k8sClient.Delete, replacement)

					for i := 0; i < 3; i++ {
						cpNode := cpNodeWithOwnerList[i]
						cpNode.Annotations[machineAnnotationOpenshift] = fmt.Sprintf("%s/%s", machineNamespace, replacementName)
						Expect(k8sClient.Update(context.Background(), &cpNode)).To(Succeed())
					}

					// Now the remediation should be completed
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFinishedMachineDeleted},
						{commonconditions.SucceededType, metav1.ConditionTrue, remediationFinishedMachineDeleted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", true},
					})
				})
			})

			When("worker node remediation exists", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(workerNode.Name)
				})
				It("worker machine is deleted", func() {
					verifyMachineIsDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)

					// Machine is deleted, but the remediation is not completed yet
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionTrue, remediationStarted},
						{commonconditions.SucceededType, metav1.ConditionUnknown, remediationStarted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})

					// Mock Machine and Node re-provisioning (even though this test does not actually delete the node, just the machine).
					// 1. Create a Machine's replacement with a new name
					// 2. Update WorkerNode's annotation to point to the new Machine
					machineReplacementName := workerNodeMachineName + "-replacement"
					workerNodeMachineReplacement := createMachineWithOwner(machineReplacementName, machineSet)
					Expect(k8sClient.Create(context.Background(), workerNodeMachineReplacement)).To(Succeed())
					DeferCleanup(k8sClient.Delete, workerNodeMachineReplacement)

					workerNode.Annotations[machineAnnotationOpenshift] = fmt.Sprintf("%s/%s", machineNamespace, machineReplacementName)
					Expect(k8sClient.Update(context.Background(), workerNode)).To(Succeed())

					// Now the remediation should be completed
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFinishedMachineDeleted},
						{commonconditions.SucceededType, metav1.ConditionTrue, remediationFinishedMachineDeleted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", true},
						{v1.EventTypeNormal, "RemediationFinished", "Remediation finished", true},
					})
				})
			})

			When("creating a resource in baremetal provider", func() {
				BeforeEach(func() {
					setMachineProviderID(workerNodeMachine, "baremetal:///dummy-provider-ID")
					underTest = createRemediationOwnedByNHC(workerNode.Name)

				})
				It("sets PermanentNodeDeletionExpected condition to false", func() {
					verifyConditionMatches(commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionFalse, v1alpha1.MachineDeletionOnBareMetalProviderReason)
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal,
							"PermanentNodeDeletionExpected",
							"Machine will be deleted and the unhealthy node replaced. This is a BareMetal cluster provider: the new node is NOT expected to have a new name",
							true},
					})
				})
			})

			When("creating a resource in cloud provider", func() {
				BeforeEach(func() {
					setMachineProviderID(workerNodeMachine, "cloud:///dummy-provider-ID")
					underTest = createRemediationOwnedByNHC(workerNode.Name)

				})
				It("sets PermanentNodeDeletionExpected condition to true", func() {
					verifyConditionMatches(commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionTrue, v1alpha1.MachineDeletionOnCloudProviderReason)
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal,
							"PermanentNodeDeletionExpected",
							"Machine will be deleted and the unhealthy node replaced. This is a Cloud cluster provider: the new node is expected to have a new name",
							true},
					})
				})
			})

			// This should never happen, but it is covered in the code
			When("creating a resource in an unknown provider", func() {
				BeforeEach(func() {
					// do not set the providerID in Machine
					underTest = createRemediationOwnedByNHC(workerNode.Name)

				})
				It("sets PermanentNodeDeletionExpected condition to false", func() {
					verifyConditionMatches(commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason)
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal,
							"PermanentNodeDeletionExpected",
							"Machine will be deleted and the unhealthy node replaced. Unknown cluster provider: no information about the new node's name",
							true},
					})
				})
			})
		})

		Context("Rainy (Error) Flows", func() {
			When("remediation is not connected to a node", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(phantomNode.Name)
				})

				It("node not found error", func() {
					Eventually(func() bool {
						return plogs.Contains(nodeNotFoundErrorMsg)
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedNodeNotFound},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedNodeNotFound}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedNodeNotFound", "failed to fetch node", true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("node does not have annotations", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(masterNode.Name)
					masterNode.Annotations = nil
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})

				It("no annotations error", func() {
					Eventually(func() bool {
						return plogs.Contains(fmt.Sprintf(noAnnotationsError, underTest.Name))
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFailed},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationFailed}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationFailed", unrecoverableError.Error(), true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("node does not have machine annotation", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(masterNode.Name)
					masterNode.Annotations[machineAnnotationOpenshift] = ""
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})

				It("no machine annotation error", func() {
					Eventually(func() bool {
						return plogs.Contains(fmt.Sprintf(noMachineAnnotationError, underTest.Name))
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFailed},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationFailed}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationFailed", unrecoverableError.Error(), true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("node's machine annotation has invalid value", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(masterNode.Name)
					masterNode.Annotations[machineAnnotationOpenshift] = "Gibberish"
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})

				It("failed to extract Name/Namespace from machine annotation error", func() {
					Eventually(func() bool {
						return plogs.Contains(fmt.Sprintf(invalidValueMachineAnnotationError, underTest.Name))
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFailed},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationFailed}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationFailed", unrecoverableError.Error(), true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("machine pointed to by node's annotation does not exist", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHC(masterNode.Name)
					masterNode.Annotations[machineAnnotationOpenshift] = "phantom-machine-namespace/phantom-machine-name"
					Expect(k8sClient.Update(context.Background(), masterNode)).ToNot(HaveOccurred())
				})

				It("failed to fetch machine error", func() {
					Eventually(func() bool {
						return plogs.Contains(machineNotFoundErrorMsg)
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationSkippedMachineNotFound},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationSkippedMachineNotFound}})
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationSkippedMachineNotFound", "failed to fetch machine of node", true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("Remediation has incorrect annotation", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHCWithAnnotation(masterNode.Name, MachineNameNsAnnotation, "Gibberish")
				})

				It("fails to follow machine deletion", func() {
					Eventually(func() bool {
						return plogs.Contains("could not get Machine data from remediation")
					}, 30*time.Second, 1*time.Second).Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFailed},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationFailed}})
					// This is not expected in a real scenario. The CR was created with a faulty annotation from the
					// beginning. As a result, the first attempt to get the Machine is via CR's annotation, it fails,
					// and the condition cannot be set. Normally, the first attempt is via Node's annotation instead.
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeWarning, "RemediationFailed", unrecoverableError.Error(), true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("machine associated to worker node fails deletion", func() {
				BeforeEach(func() {
					cclient.onDeleteError = fmt.Errorf(mockDeleteFailMessage)
					DeferCleanup(func() {
						cclient.onDeleteError = nil
					})
					underTest = createRemediationOwnedByNHC(workerNode.Name)
				})

				It("returns the same delete failure error", func() {
					Eventually(func() bool {
						return plogs.Contains(mockDeleteFailMessage)
					}, "10s", "1s").Should(BeTrue())

					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionTrue, remediationStarted},
						{commonconditions.SucceededType, metav1.ConditionUnknown, remediationStarted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})

			When("NHC stops the remediation", func() {
				BeforeEach(func() {
					underTest = createRemediationOwnedByNHCWithAnnotation(workerNode.Name, commonannotations.NhcTimedOut, "some timestamp")
				})

				It("returns without completing remediation", func() {
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationTimedOutByNhc},
						{commonconditions.SucceededType, metav1.ConditionFalse, remediationTimedOutByNhc}})
					// This is not expected in a real scenario. The CR was created with a faulty annotation from the
					// beginning. As a result, the first attempt to get the Machine is via CR's annotation, it fails,
					// and the condition cannot be set. Normally, the first attempt is via Node's annotation instead.
					verifyConditionUnset(commonconditions.PermanentNodeDeletionExpectedType)
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStopped", "NHC added the timed-out annotation, remediation will be stopped", true},
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", false},
					})
				})
			})
		})

		Context("Support to MHC created CR", func() {
			When("Machine's node exists", func() {
				BeforeEach(func() {
					// The actual remediation name should be the same as the Machine's name, however
					// the test use a different name to make sure the target Machine is not found looking at the
					// remediation's name.
					underTest = createRemediationOwnedByMHC("remediation-name", workerNodeMachine)
				})

				It("MHC worker machine is deleted", func() {
					verifyMachineIsDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)

					// Machine is deleted, but the remediation is not completed yet
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionTrue, remediationStarted},
						{commonconditions.SucceededType, metav1.ConditionUnknown, remediationStarted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})

					// Mock Machine and Node re-provisioning (even though this test does not actually delete the node, just the machine).
					// 1. Create a Machine's replacement with a new name
					// 2. Update WorkerNode's annotation to point to the new Machine
					machineReplacementName := workerNodeMachineName + "-replacement"
					workerNodeMachineReplacement := createMachineWithOwner(machineReplacementName, machineSet)
					Expect(k8sClient.Create(context.Background(), workerNodeMachineReplacement)).To(Succeed())
					DeferCleanup(k8sClient.Delete, workerNodeMachineReplacement)

					workerNode.Annotations[machineAnnotationOpenshift] = fmt.Sprintf("%s/%s", machineNamespace, machineReplacementName)
					Expect(k8sClient.Update(context.Background(), workerNode)).To(Succeed())

					// Now the remediation should be completed
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFinishedMachineDeleted},
						{commonconditions.SucceededType, metav1.ConditionTrue, remediationFinishedMachineDeleted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", true},
					})
				})
			})

			When("Machine's node does not exist", func() {
				BeforeEach(func() {
					// The actual remediation name should be the same as the Machine's name, however
					// the test use a different name to make sure the target Machine is not found looking at the
					// remediation's name.
					underTest = createRemediationOwnedByMHC("remediation-name", phantomNodeMachine)
				})

				It("MHC worker machine is deleted", func() {
					verifyMachineIsDeleted(phantomNodeMachineName)
					verifyMachineNotDeleted(workerNodeMachineName)
					verifyMachineNotDeleted(masterNodeMachineName)

					// Machine is deleted and MDR does not have to wait for the node count restoration, so the
					// remediation should be completed already.
					verifyConditionsMatch([]expectedCondition{
						{commonconditions.ProcessingType, metav1.ConditionFalse, remediationFinishedMachineDeleted},
						{commonconditions.SucceededType, metav1.ConditionTrue, remediationFinishedMachineDeleted},
						// Cluster provider is not set in this test
						{commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionUnknown, v1alpha1.MachineDeletionOnUndefinedProviderReason}})
					verifyEvents([]expectedEvent{
						{v1.EventTypeNormal, "RemediationStarted", "Remediation started", true},
					})
				})
			})
		})
	})
})

func createRemediationOwnedByNHC(remediationName string) *v1alpha1.MachineDeletionRemediation {
	mdr := &v1alpha1.MachineDeletionRemediation{}
	mdr.Name = remediationName
	mdr.Namespace = defaultNamespace
	mdr.SetOwnerReferences([]metav1.OwnerReference{
		{
			Name:       remediationName,
			Kind:       "NodeHealthCheck",
			UID:        "1234",
			APIVersion: "remediation.medik8s.io/v1alpha1",
		},
	})
	return mdr
}

func createRemediationOwnedByMHC(remediationName string, owner *machinev1beta1.Machine) *v1alpha1.MachineDeletionRemediation {
	mdr := &v1alpha1.MachineDeletionRemediation{}
	mdr.Name = remediationName
	mdr.Namespace = machineNamespace
	mdr.SetOwnerReferences([]metav1.OwnerReference{
		{
			Name:       owner.Name,
			Kind:       "Machine",
			UID:        "1234",
			APIVersion: "machine.openshift.io/v1beta1",
		},
	})
	return mdr
}

func createRemediationOwnedByNHCWithAnnotation(remediationName string, key, annotation string) *v1alpha1.MachineDeletionRemediation {
	mdr := createRemediationOwnedByNHC(remediationName)
	annotations := make(map[string]string, 1)
	annotations[key] = fmt.Sprintf("%s", annotation)
	mdr.SetAnnotations(annotations)
	return mdr
}

func createNodeWithMachine(nodeName string, machine *machinev1beta1.Machine) *v1.Node {
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

// createMachineSet creates a MachineSet with the given name.
func createMachineSet(machineSetName string, replicas int32) *machinev1beta1.MachineSet {
	machineSet := &machinev1beta1.MachineSet{}
	machineSet.SetNamespace(machineNamespace)
	machineSet.SetName(machineSetName)
	machineSet.Spec.Replicas = ptr.To[int32](replicas)
	return machineSet
}

// createControlPlaneMachineSet creates a ControlPlaneMachineSet with the given name.
func createControlPlaneMachineSet(name string) *machinev1.ControlPlaneMachineSet {
	// see https://github.com/openshift/cluster-api-actuator-pkg/blob/master/testutils/resourcebuilder/machine/v1/control_plane_machine_set.go
	cpms := &machinev1.ControlPlaneMachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: machineNamespace,
		},
		Spec: machinev1.ControlPlaneMachineSetSpec{
			Replicas: ptr.To[int32](3),
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machine.openshift.io/cluster-api-machine-role": "master",
					"machine.openshift.io/cluster-api-machine-type": "master",
					"machine.openshift.io/cluster-api-cluster":      "cluster-test-id",
				},
			},
			Template: machinev1.ControlPlaneMachineSetTemplate{
				MachineType: machinev1.OpenShiftMachineV1Beta1MachineType,
				OpenShiftMachineV1Beta1Machine: &machinev1.OpenShiftMachineV1Beta1MachineTemplate{
					ObjectMeta: machinev1.ControlPlaneMachineSetTemplateObjectMeta{
						Labels: map[string]string{
							"machine.openshift.io/cluster-api-machine-role": "master",
							"machine.openshift.io/cluster-api-cluster":      "master",
							"machine.openshift.io/cluster-api-machine-type": "master",
						},
					},
				},
			},
		},
	}

	return cpms
}

func createMachine(machineName string) *machinev1beta1.Machine {
	machine := &machinev1beta1.Machine{}
	machine.SetNamespace(machineNamespace)
	machine.SetName(machineName)
	return machine
}

// createMachineWithOwner creates a Machine with the given owner.
// the owner can be a MachineSet or a ControlPlaneMachineSet
func createMachineWithOwner(machineName string, owner metav1.Object) *machinev1beta1.Machine {
	machine := createMachine(machineName)
	var kind, apiVersion string

	switch owner.(type) {
	case *machinev1beta1.MachineSet:
		kind = machineSetKind
		apiVersion = machinev1beta1.SchemeGroupVersion.String()
	case *machinev1.ControlPlaneMachineSet:
		kind = cpmsKind
		apiVersion = machinev1.SchemeGroupVersion.String()
	default:
		panic("owner must be of type MachineSet or ControlPlaneMachineSet")
	}
	controllerVal := true
	ref := metav1.OwnerReference{
		Name:       owner.GetName(),
		Kind:       kind,
		UID:        "1234",
		APIVersion: apiVersion,
		Controller: &controllerVal,
	}
	machine.SetOwnerReferences([]metav1.OwnerReference{ref})
	return machine
}

func createDummyMachine() *machinev1beta1.Machine {
	return createMachine(dummyMachine)
}

func verifyMachineNotDeleted(machineName string) {
	Consistently(
		func() error {
			return k8sClient.Get(context.Background(), client.ObjectKey{Namespace: machineNamespace, Name: machineName}, createDummyMachine())
		}).ShouldNot(HaveOccurred(), "Machine %s should not have been deleted", machineName)
}

func verifyMachineIsDeleted(machineName string) {
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: machineNamespace, Name: machineName}, createDummyMachine()))
	}).Should(BeTrue(), "Machine %s should have been deleted", machineName)
}

func deleteIgnoreNotFound() func(ctx context.Context, obj client.Object) error {
	return func(ctx context.Context, obj client.Object) error {
		if err := k8sClient.Delete(ctx, obj); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}
}

func verifyConditionMatches(conditionType string, conditionStatus metav1.ConditionStatus, reason conditionChangeReason) {
	msg := fmt.Sprintf("Verifying that Condition '%v' is '%v' because '%v'", conditionType, conditionStatus, reason)
	By(msg)

	mdr := &v1alpha1.MachineDeletionRemediation{}
	Eventually(func() string {
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), mdr); err != nil {
			return noMachineDeletionRemediationCRFound
		}
		gotCondition := meta.FindStatusCondition(mdr.Status.Conditions, conditionType)
		if gotCondition == nil {
			return processingConditionNotSetError
		}
		if !meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, conditionType, conditionStatus) {
			return processingConditionSetButNoMatchError
		}

		if gotCondition.Reason != string(reason) {
			return processingConditionSetButWrongReasonError
		}

		return processingConditionSetAndMatchSuccess
	}, "60s", "10s").Should(Equal(processingConditionSetAndMatchSuccess), "'%v' status condition was expected to be %v and reason %v", conditionType, conditionStatus, reason)
}

func verifyConditionsMatch(expectedConditions []expectedCondition) {
	for _, e := range expectedConditions {
		verifyConditionMatches(e.conditionType, e.conditionStatus, e.conditionReason)
	}
}

func verifyConditionUnset(conditionType string) {
	msg := fmt.Sprintf("Verifying that Condition %v is unset", conditionType)
	By(msg)
	mdr := &v1alpha1.MachineDeletionRemediation{}
	err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), mdr)
	Expect(err).To(BeNil())

	gotCondition := meta.FindStatusCondition(mdr.Status.Conditions, conditionType)
	Expect(gotCondition).To(BeNil())
}

func setStopRemediationAnnotation() {
	key := client.ObjectKey{
		Name:      underTest.Name,
		Namespace: underTest.Namespace,
	}

	ExpectWithOffset(1, k8sClient.Get(context.Background(), key, underTest)).To(Succeed())

	annotations := underTest.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[commonannotations.NhcTimedOut] = time.Now().Format(time.RFC3339)
	underTest.SetAnnotations(annotations)

	Expect(k8sClient.Update(context.Background(), underTest)).ToNot(HaveOccurred())
}

func setMachineProviderID(machine *machinev1beta1.Machine, providerID string) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(machine), machine)).To(Succeed())
	})
	k8sClient.Get(context.Background(), client.ObjectKeyFromObject(machine), machine)

	machine.Spec.ProviderID = &providerID
	Expect(k8sClient.Update(context.TODO(), machine)).To(Succeed())
}

type expectedEvent struct {
	eventType, reason, message string
	expected                   bool
}

func verifyEvents(expectedEvents []expectedEvent) {
	By("verifying that the Events are emitted (or not) as expected")

	// building events message map
	toBeTestedEventsMap := make(map[string]bool)
	for _, e := range expectedEvents {
		formattedMessage := fmt.Sprintf("%s %s [remediation] %s", e.eventType, e.reason, e.message)
		toBeTestedEventsMap[formattedMessage] = e.expected
	}
	actuallyReceivedEventMap := make(map[string]bool)
	for {
		select {
		case got := <-fakeRecorder.Events:
			if _, exist := toBeTestedEventsMap[got]; exist {
				actuallyReceivedEventMap[got] = true
			}
			continue
		case <-time.After(1 * time.Second):
		}
		break

	}
	for formattedEventMessage, expected := range toBeTestedEventsMap {
		_, exists := actuallyReceivedEventMap[formattedEventMessage]
		Expect(exists).To(Equal(expected), "event test failed.\nEvent '%s': expected %v, received %v", formattedEventMessage, expected, exists)
	}
}
