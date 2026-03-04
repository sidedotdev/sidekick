package callers

// CycleA and CycleB form a mutual recursion cycle with no workflow context.
func CycleA() {
	CycleB()
}