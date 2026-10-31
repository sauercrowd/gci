package host

import (
	"strings"
	"testing"
)

func TestEnvoyBootstrapWatchesDynamicResources(t *testing.T) {
	got := envoyBootstrap(9901)
	for _, want := range []string{envoyConfigMount + "/lds/lds.json", envoyConfigMount + "/cds/cds.json", "port_value: 9901"} {
		if !strings.Contains(got, want) {
			t.Errorf("bootstrap config missing %q", want)
		}
	}
}
