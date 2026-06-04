package v1alpha1

import "testing"

func TestReplicasForAvailability(t *testing.T) {
	tests := []struct {
		name string
		in   AvailabilityType
		want int32
	}{
		{"standard -> 2", AvailabilityStandard, 2},
		{"high -> 3", AvailabilityHigh, 3},
		{"empty defaults to 2", AvailabilityType(""), 2},
		{"unknown defaults to 2", AvailabilityType("weird"), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReplicasForAvailability(tt.in); got != tt.want {
				t.Errorf("ReplicasForAvailability(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestAvailabilityConstants(t *testing.T) {
	if AvailabilityStandard != "standard" {
		t.Errorf("AvailabilityStandard = %q, want standard", AvailabilityStandard)
	}
	if AvailabilityHigh != "high" {
		t.Errorf("AvailabilityHigh = %q, want high", AvailabilityHigh)
	}
}

func TestDatabaseConstants(t *testing.T) {
	if DatabasePostgres != "postgres" || DatabaseRedis != "redis" {
		t.Errorf("unexpected database constants: %q %q", DatabasePostgres, DatabaseRedis)
	}
}

func TestNetworkConstants(t *testing.T) {
	if NetworkSepolia != "sepolia" || NetworkMainnet != "mainnet" {
		t.Errorf("unexpected network constants: %q %q", NetworkSepolia, NetworkMainnet)
	}
}

func TestShopDeepCopy(t *testing.T) {
	r := int32(3)
	orig := &Shop{Spec: ShopSpec{Name: "x", Availability: AvailabilityHigh, Replicas: &r}}
	cp := orig.DeepCopy()
	*cp.Spec.Replicas = 99
	if *orig.Spec.Replicas != 3 {
		t.Errorf("DeepCopy did not isolate Replicas pointer; orig changed to %d", *orig.Spec.Replicas)
	}
}
