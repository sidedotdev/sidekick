package fixture

func OtherCaller() string {
	return Target() + " other"
}

func ShadowedCaller() string {
	Target := func() string { return "shadow" }
	return Target() + " shadow"
}
