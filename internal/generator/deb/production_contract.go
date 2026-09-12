package deb

var productionArchitectures = [...]string{"all", "amd64"}

// ProductionArchitectures returns the ordered architecture contract for the
// initial Frostyard production suites. Callers receive a copy.
func ProductionArchitectures() []string {
	architectures := make([]string, len(productionArchitectures))
	copy(architectures, productionArchitectures[:])
	return architectures
}

func isProductionArchitecture(architecture string) bool {
	for _, allowed := range productionArchitectures {
		if architecture == allowed {
			return true
		}
	}
	return false
}
