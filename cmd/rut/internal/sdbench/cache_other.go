//go:build !darwin && !linux

package sdbench

import "os"

func cacheMethod() string           { return "uncontrolled" }
func prepareCache(f *os.File) error { return f.Sync() }
