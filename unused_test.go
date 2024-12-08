package main

import "testing"

func TestUnusedSecuritySchemes01(t *testing.T) {
	runExpandRefs(t, "testdata/80-unused-securityschemes-01")
}
