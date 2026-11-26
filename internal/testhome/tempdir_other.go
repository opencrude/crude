//go:build !darwin && !windows

package testhome

func normalizeTempDir() error { return nil }
