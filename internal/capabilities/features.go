package capabilities

const FeatureSnapshots = "feature:snapshots"

// These dependencies describe implemented host operations, not permissions
// supplied by a bundle. Budget, readiness and ownership checks remain separate.
var operationFeatures = map[string]string{
	"engine.snapshot_request": FeatureSnapshots,
	"engine.snapshot_list":    FeatureSnapshots,
	"engine.snapshot_inspect": FeatureSnapshots,
	"engine.restore_request":  FeatureSnapshots,
}

func (p Policy) Allows(ref string) bool { return contains(p.AllowedRefs, ref) }

// AllowsOperationFeatures checks only the operation's feature prerequisites.
// It does not replace operation admission or per-request authorization.
func (p Policy) AllowsOperationFeatures(operation string) bool {
	ref, required := operationFeatures[operation]
	return !required || p.Allows(ref)
}
