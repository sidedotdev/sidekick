package common

func GetTemporalServerPort() int { return 18855 }

func GetTemporalServerHostPort() string { return "127.0.0.1:18855" }

func GetTemporalClientHostPort() string { return GetTemporalServerHostPort() }

func GetTemporalDiagnosticHostPort() string { return "127.0.0.1:21855" }