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

	"github.com/go-logr/logr"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	//fetch the remediation
	var remediation *v1alpha1.MachineDeletionRemediation
	if remediation = r.getRemediation(ctx, req); remediation == nil {
		return ctrl.Result{}, nil
	}

	var machine *unstructured.Unstructured
	//Health check was done by NHC
	if node, err := r.getNodeFromMdr(remediation); err == nil {
		if machine, err = r.buildMachineFromNode(node); err != nil {
			r.Log.Error(err, "failed to fetch machine of node", "node name", node.Name)
			return ctrl.Result{}, err
		}
		if !hasControllerOwner(machine) {
			r.Log.Info("ignoring remediation of machine associated to node, since the machine has no controller owner", "node name", remediation.Name)
			return ctrl.Result{}, nil
		}

	} else { //Failed fetching the node
		r.Log.Error(err, "failed to fetch node", "node name", remediation.Name)
		return ctrl.Result{}, err
	}

	log.Info("reconciling", "node", remediation.Name, "associated machine", machine.GetName())
	if err := r.deleteMachineOfNode(ctx, machine, remediation.Name); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MachineDeletionRemediationReconciler) deleteMachineOfNode(ctx context.Context, machine *unstructured.Unstructured, nodeName string) error {
	//delete the machine
	if err := r.Client.Delete(ctx, machine); err != nil {
		r.Log.Error(err, "failed to delete machine associated to node", "node name", nodeName)
		return err
	}

	key := client.ObjectKey{
		Name:      machine.GetName(),
		Namespace: machine.GetNamespace(),
	}

	//verify machine is deleted
	if err := r.Get(context.TODO(), key, machine); !errors.IsNotFound(err) {
		r.Log.Info("machine associated to node was not deleted probably due to a finalizer on the machine, note that the remediation is pending", "node name", nodeName)
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

func (r *MachineDeletionRemediationReconciler) getRemediation(ctx context.Context, req ctrl.Request) *v1alpha1.MachineDeletionRemediation {
	remediation := new(v1alpha1.MachineDeletionRemediation)
	key := client.ObjectKey{Name: req.Name, Namespace: req.Namespace}
	if err := r.Client.Get(ctx, key, remediation); err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "error retrieving remediation in namespace", "remediation name", req.Name, "namespace", req.Namespace)
		}
		return nil
	}
	return remediation
}

func (r *MachineDeletionRemediationReconciler) getNodeFromMdr(mdr *v1alpha1.MachineDeletionRemediation) (*v1.Node, error) {
	node := &v1.Node{}
	key := client.ObjectKey{
		Name: mdr.Name,
	}

	if err := r.Get(context.Background(), key, node); err != nil {
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

func extractNameAndNamespace(nameNamespace string, nodeName string) (string, string, error) {
	if nameNamespaceSlice := strings.Split(nameNamespace, "/"); len(nameNamespaceSlice) == 2 {
		return nameNamespaceSlice[1], nameNamespaceSlice[0], nil
	}
	return "", "", fmt.Errorf(invalidValueMachineAnnotationError, nodeName)
}
