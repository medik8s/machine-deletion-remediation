# Machine-API Driven Remediation

This operator conforms to the External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme) and is designed to work with Node Health Check (link) to reprovision unhealthy nodes using the Machine API (link). It functions by following the annotation on the Node to the associated Machine object, confirms that it has an owning controller (eg. MachineSetController), and deletes it.  Once the Machine CR has been deleted, the owning controller creates a replacement. 

## Pre-requisites
* Machine API based cluster that is able to programmatically destroy and create cluster nodes
* Nodes are associated with Machines
* Machines are declaratively managed
* Node Healthheck is installed and running 

## Installation
- Deploy MDR (Machine-deletion-remediation) to a container in the cluster pod.  Try `make deploy`, official images comming soon.
- Load the yaml manifest of the MDR template.
- Modifying NHC CR so it'll use MDR as it's remediator.
This is basically a specific use case of an External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme).
In order to set up: make sure that Node Health Check is running, Machine-deletion-remediation controller exists and then create the necessary CRs.

## Example CRs
NodeHealthCheck:
```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
  remediationTemplate:
    kind: MachineDeletionRemediationTemplate
    apiVersion: machine.remediation.medik8s.io/v1alpha1
    name: group-x
    namespace: default
```
The remediation template object: this CR is created by the admin, it is used as a template by NHC for creating the remediation object.
MachineDeletionRemediationTemplate:
```yaml
   apiVersion: machine.remediation.medik8s.io/v1alpha1
   kind: MachineDeletionRemediationTemplate
   metadata:
     name: group-x
     namespace: default
   spec:
     template:
       spec: {}
```
A remediation object: these CRs are created by NHC when [...]. The MDR operator watches for them and [...].
MachineDeletionRemediation:
```yaml
apiVersion: machine.remediation.medik8s.io/v1alpha1
kind: MachineDeletionRemediation
metadata:
  # named after the machine
  name: machine-sample
  namespace: default
  ownerReferences:
    - kind: Machine
      apiVersion: remediation.medik8s.io/v1alpha1
      name: machine-sample
spec: {}
```
