//go:build darwin

package platform

func Current() Host { return Darwin{} }
