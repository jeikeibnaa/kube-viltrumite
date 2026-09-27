package controller

// Manager-wide permissions that no single reconciler owns.
//
// Leader election (--leader-elect) keeps its Lease in the operator's own
// namespace, so that grant is a namespaced Role: write access to every Lease in
// the cluster would let the operator disturb other components' leader election.
// Events are recorded in the namespace of the object they describe, so that
// grant stays cluster-wide.
//
//+kubebuilder:rbac:groups=coordination.k8s.io,namespace=viltrumite-system,resources=leases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
