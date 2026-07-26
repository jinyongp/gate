//go:build linux

package platform

func Current() Host { return Linux{} }
