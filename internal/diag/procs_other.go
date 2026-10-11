//go:build !windows

package diag

// На других системах приложение не живёт; снимок только про сеть.
func sampleProcs() map[string]procSample { return map[string]procSample{} }

func systemVPN() []string { return nil }
