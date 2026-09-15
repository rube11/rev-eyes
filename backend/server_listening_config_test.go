package main

import "testing"

func TestServerMoonshineDefaultsOn(t *testing.T) {
	for _, value := range []string{"", "true", " TRUE "} {
		if !serverMoonshineEnabled(value) {
			t.Fatalf("%q disabled default server listening", value)
		}
	}
	for _, value := range []string{"false", " FALSE "} {
		if serverMoonshineEnabled(value) {
			t.Fatalf("%q did not disable server listening", value)
		}
	}
}
