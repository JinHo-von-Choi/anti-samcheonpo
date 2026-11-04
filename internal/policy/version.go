package policy

// Changing experiment semantics must never relabel historical observations.
const Version = "intervention-policy/2"
const Experiment = "wording/2"

// None is retained as a configuration alias for facts without prescriptions.
func FactOnly(arm string) bool { return arm == "none" || arm == "fact" || arm == "fact_only" }
