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
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	machineAnnotationOpenshift = "machine.openshift.io/machine"
	machineKind                = "Machine"
	machineSetKind             = "MachineSet"
	// MachineNameNamespaceAnnotation contains to-be-deleted Machine's Name and Namespace
	MachineNameNamespaceAnnotation = "machine-deletion-remediation.medik8s.io/machineNameNamespace"
	//Errors
	noAnnotationsError                 = "failed to find machine annotation on node name: %s"
	noMachineAnnotationError           = "failed to find openshift machine annotation on node name: %s"
	invalidValueMachineAnnotationError = "failed to extract Machine Name and Machine Namespace from machine annotation on the node for node name: %s"
	failedToDeleteMachineError         = "failed to delete machine of node name: %s"
)

// MachineDeletionRemediationReconciler reconciles a MachineDeletionRemediation object
type MachineDeletionRemediationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations/finalizers,verbs=update
//+kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the MachineDeletionRemediationRemediation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *MachineDeletionRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("machinedeletionremediation", req.NamespacedName)

	log.Info("reconciling...")

	var err error
	var remediation *v1alpha1.MachineDeletionRemediation
	if remediation, err = r.getRemediation(ctx, req); remediation == nil || err != nil {
		if apiErrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	annotations := remediation.GetAnnotations()
	if annotations != nil {
		machineNameNamespace, exists := annotations[MachineNameNamespaceAnnotation]
		if exists {
			machineName, machineNamespace, err := extractNameAndNamespace(machineNameNamespace, remediation.GetName())
			if err != nil {
				log.Error(err, "could not get Machine data from remediation", "remediation", remediation.GetName(), "annotation", machineNameNamespace)
				return ctrl.Result{}, nil
			}

			ok, err := r.verifyMachineIsDeleted(ctx, machineName, machineNamespace, remediation.Name)
			if err != nil {
				return ctrl.Result{}, err
			}
			if !ok {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, nil
		}
	}

	var node *v1.Node
	if node, err = r.getNodeFromMdr(remediation); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("MDR CR and affected Node found", "node", node.GetName())

	var machine *unstructured.Unstructured
	if machine, err = r.buildMachineFromNode(node); err != nil {
		r.Log.Error(err, "failed to fetch machine of node", "node name", node.Name)
		return ctrl.Result{}, err
	}

	if !hasControllerOwner(machine) {
		log.Info("ignoring remediation of node-associated machine: the machine has no controller owner", "machine", machine.GetName(), "node name", remediation.Name)
		return ctrl.Result{}, nil
	}

	log.Info("node-associated machine found", "node", node.Name, "machine", machine.GetName())

	err = r.deleteMachineOfNode(ctx, machine, remediation.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.setDeletedMachineAnnotation(ctx, remediation, machine)
	if err != nil {
		return ctrl.Result{}, errors.Wrapf(err, "failed to update remediation CR with timeout annotation")
	}

	// requeue immediately to check machine deletion progression
	return ctrl.Result{Requeue: true}, nil
}

func (r *MachineDeletionRemediationReconciler) deleteMachineOfNode(ctx context.Context, machine *unstructured.Unstructured, nodeName string) error {
	if err := r.Client.Delete(ctx, machine); err != nil {
		r.Log.Error(err, "failed to delete machine associated to node", "node name", nodeName)
		return err
	}
	return nil
}

func hasControllerOwner(machine *unstructured.Unstructured) bool {
	refs := machine.GetOwnerReferences()
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineDeletionRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MachineDeletionRemediation{}).
		Complete(r)
}

func (r *MachineDeletionRemediationReconciler) getRemediation(ctx context.Context, req ctrl.Request) (*v1alpha1.MachineDeletionRemediation, error) {
	remediation := new(v1alpha1.MachineDeletionRemediation)
	key := client.ObjectKey{Name: req.Name, Namespace: req.Namespace}
	if err := r.Client.Get(ctx, key, remediation); err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Info("MDR already deleted, nothing to do")
			return nil, nil
		}
		r.Log.Error(err, "could not find remediation object in namespace", "remediation name", req.Name, "namespace", req.Namespace)
		return nil, err
	}
	return remediation, nil
}

func (r *MachineDeletionRemediationReconciler) getNodeFromMdr(mdr *v1alpha1.MachineDeletionRemediation) (*v1.Node, error) {
	node := &v1.Node{}
	key := client.ObjectKey{
		Name: mdr.Name,
	}

	if err := r.Get(context.Background(), key, node); err != nil {
		r.Log.Error(err, "failed to fetch node", "node name", mdr.Name)
		return nil, err
	}
	return node, nil
}

func (r *MachineDeletionRemediationReconciler) buildMachineFromNode(node *v1.Node) (*unstructured.Unstructured, error) {

	var nodeAnnotations map[string]string
	if nodeAnnotations = node.Annotations; nodeAnnotations == nil {
		return nil, fmt.Errorf(noAnnotationsError, node.Name)
	}
	var machineNameNamespace, machineName string

	//OpenShift Machine
	if machineNameNamespace = nodeAnnotations[machineAnnotationOpenshift]; len(machineNameNamespace) == 0 {
		return nil, fmt.Errorf(noMachineAnnotationError, node.Name)
	}

	machineName, machineNamespace, err := extractNameAndNamespace(machineNameNamespace, node.Name)
	if err != nil {
		return nil, err
	}

	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(v1beta1.SchemeGroupVersion.String())

	key := client.ObjectKey{
		Name:      machineName,
		Namespace: machineNamespace,
	}

	if err := r.Get(context.TODO(), key, machine); err != nil {
		return nil, err
	}
	return machine, nil
}

func (r *MachineDeletionRemediationReconciler) verifyMachineIsDeleted(ctx context.Context, machineName, MachineNamespace, nodeName string) (bool, error) {
	key := client.ObjectKey{
		Name:      machineName,
		Namespace: MachineNamespace,
	}

	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(v1beta1.SchemeGroupVersion.String())
	if err := r.Get(ctx, key, machine); err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Info("node-associated machine correctly deleted", "node", nodeName, "machine", machineName)
			return true, nil
		}
		r.Log.Error(err, "unexpected error retrieving the node-associated machine after deletion request", "node", nodeName, "machine", key.Name)
		return false, err
	}

	machinePhase, err := getMachineStatusPhase(machine)
	if err != nil {
		r.Log.Error(err, "could not get machine's phase")
		machinePhase = "unknown"
	}

	r.Log.Info("node-associated machine was not deleted yet", "node", nodeName, "machine", machineName, "machine status.phase", machinePhase)
	return false, nil
}

func (r *MachineDeletionRemediationReconciler) setDeletedMachineAnnotation(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation, machine *unstructured.Unstructured) error {
	annotations := remediation.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}

	annotations[MachineNameNamespaceAnnotation] = fmt.Sprintf("%s/%s", machine.GetNamespace(), machine.GetName())
	remediation.SetAnnotations(annotations)

	return r.Update(ctx, remediation)
}

func extractNameAndNamespace(nameNamespace string, nodeName string) (string, string, error) {
	if nameNamespaceSlice := strings.Split(nameNamespace, "/"); len(nameNamespaceSlice) == 2 {
		return nameNamespaceSlice[1], nameNamespaceSlice[0], nil
	}
	return "", "", fmt.Errorf(invalidValueMachineAnnotationError, nodeName)
}

func getMachineStatusPhase(machine *unstructured.Unstructured) (string, error) {
	status, ok, err := unstructured.NestedMap(machine.Object, "status")
	if err != nil {
		return "", fmt.Errorf("could not get Machine's status: error %w", err)
	}
	if !ok {
		return "", fmt.Errorf("Machine object does not have a status field")
	}

	phase, ok, err := unstructured.NestedString(status, "phase")
	if err != nil {
		return "", fmt.Errorf("could not get Machine's status.phase: error %w", err)
	}
	if !ok {
		return "", fmt.Errorf("Machine object does not have a status.phase field")
	}
	return phase, nil
}
