package policy

import (
	"fmt"
	"testing"
)

func BenchmarkSourceIPPolicyExactAllowsAddr(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules_%d", size), func(b *testing.B) {
			rules := make([]string, 0, size)
			for i := 0; i < size; i++ {
				rules = append(rules, fmt.Sprintf("10.%d.%d.%d", (i/65536)%256, (i/256)%256, i%256))
			}
			target := "10.1.2.3:443"
			if size > 0 {
				rules[size-1] = "10.1.2.3"
			}
			p := CompileSourceIPPolicy(ModeBlacklist, rules, Options{})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if p.AllowsAddr(target) {
					b.Fatal("expected blacklist match to deny target")
				}
			}
		})
	}
}

func BenchmarkSourceIPPolicyCIDRAllowsAddr(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules_%d", size), func(b *testing.B) {
			rules := make([]string, 0, size)
			for i := 0; i < size; i++ {
				rules = append(rules, fmt.Sprintf("10.%d.%d.0/24", (i/256)%256, i%256))
			}
			target := "10.1.2.9:443"
			if size > 0 {
				rules[size-1] = "10.1.2.0/24"
			}
			p := CompileSourceIPPolicy(ModeBlacklist, rules, Options{})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if p.AllowsAddr(target) {
					b.Fatal("expected cidr blacklist match to deny target")
				}
			}
		})
	}
}
